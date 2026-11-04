//go:build unix

package distribution_test

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
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	"github.com/stretchr/testify/require"
)

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

// freePorts returns n different free ports. The listeners stay open until every port is chosen, so that no two of them
// are the same port.
func freePorts(t *testing.T, n int) []uint16 {
	t.Helper()
	ports := make([]uint16, 0, n)
	for len(ports) < n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer l.Close()
		ports = append(ports, uint16(l.Addr().(*net.TCPAddr).Port))
	}
	return ports
}

// waitReady waits until the readiness probe answers 200. It fails at once, with the error, when the runtime stopped
// before it was ready, and otherwise says what the last probe saw: a probe that never answers must not hide behind one
// timeout of the whole wait.
func waitReady(t *testing.T, probe string, stopped <-chan error) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	last := "no probe was made"
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		select {
		case err := <-stopped:
			t.Fatalf("the runtime stopped before both sources made it ready: %v", err)
		default:
		}
		resp, err := client.Get(probe) //nolint:gosec,noctx // local test endpoint
		if err != nil {
			last = "error: " + err.Error()
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return
		}
		last = "status " + resp.Status
	}
	t.Fatalf("readiness covers both sources: not ready after 30s; the last probe of %s saw %s", probe, last)
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
	ports := freePorts(t, 4)
	managementPort := ports[0]

	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), "test", runtime.Config{
		ManagementPort:               managementPort,
		ServicePort:                  ports[1],
		OfrepServicePort:             ports[2],
		SyncServicePort:              ports[3],
		StreamDeadline:               5 * time.Second,
		KeepAliveMinTime:             runtime.KeepAliveMinTimeDefault,
		KeepAlivePermitWithoutStream: runtime.KeepAlivePermitWithoutStreamDefault,
		SyncProviders: []coresync.SourceConfig{
			{URI: flagdSource, Provider: "file"},
			{URI: vendorSource, Provider: "file", Format: keyvalue.Type},
		},
		ConfigurationFormats: []configformat.Factory{factory},
	}, flagdcomponents.Components())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- rt.Start() }()

	waitReady(t, fmt.Sprintf("http://127.0.0.1:%d/readyz", managementPort), done)

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
	}, flagdcomponents.Components())
	require.NoError(t, err)

	format, named := rt.Formats[path]
	require.True(t, named, "the source names the format")
	result, err := format.Accept(context.Background(), configformat.Input{Source: path, Data: keyvalueFlags})
	require.NoError(t, err)
	require.Len(t, result.Documents, 1)
}
