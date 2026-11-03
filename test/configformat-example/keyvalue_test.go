//go:build unix

package keyvalue_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	keyvalue "github.com/open-feature/flagd/test/configformat-example"

	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat/formattest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	"github.com/stretchr/testify/require"
)

// TestContract covers the main scenario's steps 1 to 7: the example is a component module of its own
// that depends on the core module and meets every case of the configuration format contract.
func TestContract(t *testing.T) {
	formattest.Run(t, keyvalue.Factory{}, formattest.Samples{
		Valid: formattest.Delivery{
			Data: `{"environment":"prod","flags":{"a":1,"b":"two","c":true}}`,
			Sets: []formattest.Set{{FlagSet: "prod", Keys: []string{"a", "b", "c"}}},
		},
		Changed: formattest.Change{
			Delivery: formattest.Delivery{
				Data: `{"environment":"prod","flags":{"a":1,"b":"changed","d":4}}`,
				Sets: []formattest.Set{{FlagSet: "prod", Keys: []string{"a", "b", "d"}}},
			},
			FlagSet: "prod", Added: []string{"d"}, Updated: []string{"b"}, Removed: []string{"c"},
		},
		Invalid: formattest.Faulty{
			Data:    `{"environment":"prod","flags":{"a":1,"b":null}}`,
			Entries: []string{"b"},
		},
		Incremental: &[2]formattest.Delivery{
			{Data: `{"environment":"prod","flags":{"a":1}}`, Sets: []formattest.Set{{FlagSet: "prod", Keys: []string{"a"}}}},
			{Data: `{"environment":"dev","flags":{"a":1}}`, Sets: []formattest.Set{{FlagSet: "dev", Keys: []string{"a"}}}},
		},
	})
}

// TestEngineModuleExportsFormatFactory covers A5's configuration format half: the module exports the
// factory under the type name that sources name. The paired evaluator is tested with UC-007.
func TestEngineModuleExportsFormatFactory(t *testing.T) {
	var factory configformat.Factory = keyvalue.Factory{}
	require.Equal(t, "keyvalue", factory.Type())

	st, err := store.NewStore(logger.NewLogger(nil, false), []string{"src"})
	require.NoError(t, err)
	format, err := factory.Create(configformat.Settings{Store: st})
	require.NoError(t, err)
	require.Equal(t, factory.Type(), format.Type())
}

// capturingFactory hands out the store the distribution injects, so the test can read it.
type capturingFactory struct {
	keyvalue.Factory
	store *store.Store
}

func (c *capturingFactory) Create(s configformat.Settings) (configformat.ConfigurationFormat, error) {
	c.store = s.Store
	return c.Factory.Create(s)
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

const (
	flagdFlags    = `{"flags":{"f1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`
	keyvalueFlags = `{"environment":"prod","flags":{"a":1,"b":"two"}}`
)

// TestTryInDistribution covers A2 as far as the agreed gap allows (a client receiving the flags
// is tested with UC-007 and UC-017): a runtime built from configuration with the example registered
// reads a source that names it next to a flagd source, becomes ready, keeps the example's
// documents, and leaves flagd's own flags alone.
func TestTryInDistribution(t *testing.T) {
	vendorSource := writeFile(t, "vendor.json", keyvalueFlags)
	flagdSource := writeFile(t, "flagd.json", flagdFlags)
	factory := &capturingFactory{}
	managementPort := freePort(t)

	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), "test", runtime.Config{
		ManagementPort:               managementPort,
		ServicePort:                  freePort(t),
		OfrepServicePort:             freePort(t),
		SyncServicePort:              freePort(t),
		StreamDeadline:               5 * time.Second,
		KeepAliveMinTime:             runtime.KeepAliveMinTimeDefault,
		KeepAlivePermitWithoutStream: runtime.KeepAlivePermitWithoutStreamDefault,
		SyncProviders: []coresync.SourceConfig{
			{URI: flagdSource, Provider: "file"},
			{URI: vendorSource, Provider: "file", Format: keyvalue.Type},
		},
		ConfigurationFormats: []configformat.Factory{factory},
	}, runtime.Official())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- rt.Start() }()

	ready := fmt.Sprintf("http://127.0.0.1:%d/readyz", managementPort)
	require.Eventually(t, func() bool {
		resp, err := http.Get(ready) //nolint:gosec,noctx // local test endpoint
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 20*time.Second, 100*time.Millisecond, "readiness covers both sources")

	documents := factory.store.Documents(keyvalue.Type, vendorSource)
	require.Len(t, documents, 1)
	require.Equal(t, "prod", documents[0].FlagSet)
	require.Equal(t, []string{"a", "b"}, documents[0].FlagKeys)
	require.NotEmpty(t, documents[0].Version)

	flags, _, err := factory.store.GetAll(context.Background(), &store.Selector{})
	require.NoError(t, err)
	require.Len(t, flags, 1, "the example's keys never reach flagd's flags table")
	require.Equal(t, "f1", flags[0].Key)
	require.Equal(t, flagdSource, flags[0].Source)
	require.Empty(t, factory.store.Documents(keyvalue.Type, flagdSource))

	// Start stops on SIGTERM, which it handles itself
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the runtime did not stop")
	}
}

// TestSourceWithoutFormatIsRejectedByFlagd covers A3: flagd's format rejects the vendor's flag
// configuration with its own text and the source, and naming the format on the source (a
// configuration change, no rebuild) gets it accepted.
func TestSourceWithoutFormatIsRejectedByFlagd(t *testing.T) {
	const source = "vendor.json"
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{source})
	require.NoError(t, err)

	_, err = flagdformat.NewFormat(st, log).Accept(context.Background(), configformat.Input{Source: source, Data: keyvalueFlags})

	var rejection configformat.Rejection
	require.True(t, errors.As(err, &rejection), "want a rejection, got %v", err)
	require.Equal(t, source, rejection.Source)
	require.Contains(t, rejection.Reason, "unmarshalling flags map: ")
	require.Empty(t, st.Documents(keyvalue.Type, source))
	require.Empty(t, st.Documents(flagdformat.Type, source))

	path := writeFile(t, "vendor.json", keyvalueFlags)
	rt, err := runtime.FromConfig(log, "test", runtime.Config{
		SyncProviders:        []coresync.SourceConfig{{URI: path, Provider: "file", Format: keyvalue.Type}},
		ConfigurationFormats: []configformat.Factory{keyvalue.Factory{}},
	}, runtime.Official())
	require.NoError(t, err)

	format, named := rt.Formats[path]
	require.True(t, named, "the source names the format")
	result, err := format.Accept(context.Background(), configformat.Input{Source: path, Data: keyvalueFlags})
	require.NoError(t, err)
	require.Len(t, result.Documents, 1)
}
