//go:build unix

package distribution_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
	"io"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature/flagd/flagd/pkg/evaluation"
	"github.com/open-feature/flagd/flagd/pkg/runtime"
	"github.com/stretchr/testify/require"

	keyvalue "github.com/open-feature/flagd/test/configformat-example"
	keyvalueeval "github.com/open-feature/flagd/test/configformat-example/evaluator"
)

func postJSON(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return resp.StatusCode, decoded
}

func post(t *testing.T, url, body string) map[string]any {
	t.Helper()
	status, decoded := postJSON(t, url, body)
	require.Equal(t, http.StatusOK, status, "%s: %v", url, decoded)
	return decoded
}

// TestEngineServesBothFormats covers A5 and UC-018 A2's client half: a runtime built from configuration with
// the engine module's configuration format and evaluator registered serves a flagd file source and a keyvalue
// file source side by side. The flag evaluation (connect) and OFREP endpoints return both formats' flags in
// bulk, and a single resolution reaches the evaluator that owns the flag.
func TestEngineServesBothFormats(t *testing.T) {
	flagdSource := writeFile(t, "flagd.json", flagdFlags)
	vendorSource := writeFile(t, "vendor.json", `{"environment":"prod","flags":{"a":1,"b":"two","server.secret":"hush"}}`)
	managementPort, servicePort, ofrepPort := freePort(t), freePort(t), freePort(t)

	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), "test", runtime.Config{
		ManagementPort:               managementPort,
		ServicePort:                  servicePort,
		OfrepServicePort:             ofrepPort,
		SyncServicePort:              freePort(t),
		StreamDeadline:               5 * time.Second,
		KeepAliveMinTime:             runtime.KeepAliveMinTimeDefault,
		KeepAlivePermitWithoutStream: runtime.KeepAlivePermitWithoutStreamDefault,
		SyncProviders: []coresync.SourceConfig{
			{URI: flagdSource, Provider: "file"},
			{URI: vendorSource, Provider: "file", Format: keyvalue.Type},
		},
		ConfigurationFormats: []configformat.Factory{keyvalue.Factory{}},
		Evaluators:           []evaluator.Factory{keyvalueeval.Factory{}},
	}, flagdcomponents.Official())
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

	connect := fmt.Sprintf("http://127.0.0.1:%d/flagd.evaluation.v1.Service/", servicePort)
	ofrep := fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/evaluate/flags", ofrepPort)

	all := post(t, connect+"ResolveAll", `{}`)["flags"].(map[string]any)
	require.Len(t, all, 4)
	require.Contains(t, all, "f1")
	require.Contains(t, all, "a")
	require.Contains(t, all, "b")
	require.Contains(t, all, "server.secret")

	bulk := post(t, ofrep, `{}`)["flags"].([]any)
	values := map[string]any{}
	for _, entry := range bulk {
		e := entry.(map[string]any)
		values[e["key"].(string)] = e["value"]
	}
	require.Equal(t, map[string]any{"f1": true, "a": 1.0, "b": "two", "server.secret": "hush"}, values)

	require.Equal(t, true, post(t, connect+"ResolveBoolean", `{"flagKey":"f1"}`)["value"])
	require.Equal(t, "two", post(t, connect+"ResolveString", `{"flagKey":"b"}`)["value"])
	require.Equal(t, "1", post(t, connect+"ResolveInt", `{"flagKey":"a"}`)["value"], "connect JSON encodes int64 as a string")
	require.Equal(t, "two", post(t, ofrep+"/b", `{}`)["value"])

	// the type check is the adapter's, whichever evaluator resolved the flag
	status, _ := postJSON(t, connect+"ResolveBoolean", `{"flagKey":"b"}`)
	require.NotEqual(t, http.StatusOK, status, "a string flag is not a boolean")

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the runtime did not stop")
	}
}

// TestShadowedKeys covers one key held by a flagd source and a keyvalue source: the source that comes later in
// the distribution's configuration wins, whichever format it is in (FR-035).
func TestShadowedKeys(t *testing.T) {
	const flagdFile, kvFile = "flagd.json", "kv.json"
	for _, sources := range [][]string{{flagdFile, kvFile}, {kvFile, flagdFile}} {
		t.Run(fmt.Sprintf("%v", sources), func(t *testing.T) {
			log := logger.NewLogger(nil, false)
			st, err := store.NewStore(log, sources)
			require.NoError(t, err)
			ev, err := evaluation.NewRoutedEvaluator(log, st, []string{keyvalue.Type}, []evaluator.Factory{keyvalueeval.Factory{}})
			require.NoError(t, err)

			require.NoError(t, ev.SetState(coresync.DataSync{Source: flagdFile, FlagData: `{"flags":{
				"shared":{"state":"ENABLED","defaultVariant":"v","variants":{"v":"from-flagd"}},
				"flagd-only":{"state":"ENABLED","defaultVariant":"v","variants":{"v":"flagd"}}}}`}))
			format, err := keyvalue.Factory{}.Create(configformat.Settings{Store: st, Logger: log})
			require.NoError(t, err)
			_, err = format.Accept(context.Background(), configformat.Input{
				Source: kvFile, Data: `{"environment":"","flags":{"shared":"from-keyvalue","kv-only":"keyvalue"}}`,
			})
			require.NoError(t, err)

			winner := "from-flagd"
			if sources[1] == kvFile {
				winner = "from-keyvalue"
			}
			got, _, _, _, err := ev.ResolveStringValue(context.Background(), "id", "shared", nil)
			require.NoError(t, err)
			require.Equal(t, winner, got)

			bulk, _, err := ev.ResolveAllValues(context.Background(), "id", nil)
			require.NoError(t, err)
			byKey := map[string]any{}
			for _, v := range bulk {
				byKey[v.FlagKey] = v.Value
			}
			require.Equal(t, map[string]any{"shared": winner, "flagd-only": "flagd", "kv-only": "keyvalue"}, byKey)

			// the lower-priority source stays reachable through its own selector
			selector, err := store.NewSelector("source=" + sources[0])
			require.NoError(t, err)
			loser, _, _, _, err := ev.ResolveStringValue(
				context.WithValue(context.Background(), store.SelectorContextKey{}, selector), "id", "shared", nil)
			require.NoError(t, err)
			require.NotEqual(t, winner, loser)
		})
	}
}
