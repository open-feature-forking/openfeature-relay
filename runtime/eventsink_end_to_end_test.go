//go:build unix

package runtime

import (
	"fmt"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	flageval "github.com/open-feature-forking/openfeature-relay/service/flag-evaluation"
	"github.com/open-feature-forking/openfeature-relay/service/ofrep"
	"github.com/stretchr/testify/require"
)

const twoFlags = `{"flags":{
	"a":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}},
	"b":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`

// sinkSettings flushes a batch after 20ms, so a test does not wait for the default second.
func sinkSettings() map[string]any {
	return map[string]any{eventsink.KeyFlushInterval: "20ms"}
}

// settleTime is how long a test waits for an entry that must not arrive.
const settleTime = 300 * time.Millisecond

// TestSinkReceivesOneEntryPerFlag pins, end to end through a recording sink on a started runtime, that an event sink
// gets exactly one batch entry for each resolved flag: one for a single request and one for each flag of a bulk
// request, on flag evaluation (ConnectRPC) and on OFREP.
func TestSinkReceivesOneEntryPerFlag(t *testing.T) {
	p := newPorts(t)
	cfg := characterizedConfig(t, p)
	source := filepath.Join(t.TempDir(), "two.json")
	require.NoError(t, os.WriteFile(source, []byte(twoFlags), 0o600))
	cfg.SyncProviders = []coresync.SourceConfig{{URI: source, Provider: "file"}}
	sink := &recordingSink{}
	startRuntimeWith(t, cfg, withFlagd(Components{
		Endpoints:  flagdcomponents.Components().Endpoints,
		EventSinks: []eventsink.Configured{{Factory: recordingSinkFactory{sink: sink}, Config: sinkSettings()}},
	}))

	post := func(url, body string, header map[string]string) {
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, url)
	}
	// entriesAfter waits for the sink to hold n entries, then for more to show up that must not be there.
	entriesAfter := func(n int) []events.Event {
		require.Eventually(t, func() bool { return len(sink.entries()) >= n }, 10*time.Second, 10*time.Millisecond)
		time.Sleep(settleTime)
		return sink.entries()
	}
	keys := func(entries []events.Event) []string {
		var out []string
		for _, e := range entries {
			out = append(out, e.(events.Evaluation).FlagKey)
		}
		return out
	}

	connect := fmt.Sprintf("http://127.0.0.1:%d/flagd.evaluation.v1.Service/", p.service)
	json := map[string]string{"Content-Type": "application/json"}
	restful := fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/evaluate/flags", p.ofrep)

	post(connect+"ResolveBoolean", `{"flagKey":"a"}`, json)
	all := entriesAfter(1)
	require.Len(t, all, 1, "one entry for one resolved flag over ConnectRPC")
	require.Equal(t, []string{"a"}, keys(all))
	require.Equal(t, flageval.Type, all[0].(events.Evaluation).Endpoint)

	post(connect+"ResolveAll", `{}`, json)
	all = entriesAfter(3)
	require.Len(t, all, 3, "one entry for each of the two flags of a bulk request over ConnectRPC")
	require.ElementsMatch(t, []string{"a", "a", "b"}, keys(all))

	post(restful+"/b", "{}", nil)
	all = entriesAfter(4)
	require.Len(t, all, 4, "one entry for one resolved flag over OFREP")
	require.Equal(t, ofrep.Type, all[3].(events.Evaluation).Endpoint)
	require.Equal(t, "b", all[3].(events.Evaluation).FlagKey)

	post(restful, "{}", nil)
	all = entriesAfter(6)
	require.Len(t, all, 6, "one entry for each of the two flags of a bulk request over OFREP")
	require.ElementsMatch(t, []string{"a", "b"}, keys(all[4:]))
	for _, e := range all[4:] {
		require.Equal(t, ofrep.Type, e.(events.Evaluation).Endpoint)
	}
}

// TestSinkReceivesOneEntryPerDocument pins, end to end through a recording sink, that an event sink for configuration
// change events gets one entry for each document a configuration format accepted as changed, whatever the format,
// and none for a redelivery.
func TestSinkReceivesOneEntryPerDocument(t *testing.T) {
	dir := t.TempDir()
	flagdSource := filepath.Join(dir, "flags.json")
	require.NoError(t, os.WriteFile(flagdSource, []byte(changeFlags), 0o600))
	vendorSource := filepath.Join(dir, "vendor.json")
	require.NoError(t, os.WriteFile(vendorSource, []byte(`{}`), 0o600))
	cfg := characterizedConfig(t, newPorts(t))
	cfg.SyncProviders = []coresync.SourceConfig{
		{URI: flagdSource, Provider: "file"},
		{URI: vendorSource, Provider: "file", Format: "versioned"},
	}
	sink := &recordingSink{}
	rt, err := FromConfig(logger.NewLogger(nil, false), "test", cfg, listening(withFlagd(Components{
		ConfigurationFormats: []configformat.Factory{versionedFactory{}},
		EventSinks: []eventsink.Configured{{
			Factory: recordingSinkFactory{kinds: []events.Kind{events.KindConfigurationChange}, sink: sink},
			Config:  sinkSettings(),
		}},
	})))
	require.NoError(t, err)
	t.Cleanup(func() {
		rt.Bus.Close()
		rt.runShutdownHooks()
	})

	changed := `{"flags":{"a":{"state":"ENABLED","defaultVariant":"off","variants":{"on":true,"off":false}}}}`
	for _, d := range []coresync.DataSync{
		{Source: flagdSource, FlagData: changeFlags},
		{Source: flagdSource, FlagData: changeFlags}, // a redelivery
		{Source: flagdSource, FlagData: changed},
		{Source: vendorSource, FlagData: "one"},
		{Source: vendorSource, FlagData: "one"}, // a redelivery
		{Source: vendorSource, FlagData: "two"},
	} {
		rt.updateAndEmit(d)
	}

	require.Eventually(t, func() bool { return len(sink.entries()) >= 4 }, 10*time.Second, 10*time.Millisecond)
	time.Sleep(settleTime)
	var got []string
	for _, e := range sink.entries() {
		c := e.(events.ConfigurationChange)
		got = append(got, c.Format+" "+filepath.Base(c.Source)+" "+c.Version)
	}
	require.Len(t, got, 4, "one entry for each changed document: %v", got)
	require.Equal(t, []string{"vendor.json one", "vendor.json two"}, []string{
		strings.TrimPrefix(got[2], "versioned "), strings.TrimPrefix(got[3], "versioned "),
	})
	for _, g := range got[:2] {
		require.True(t, strings.HasPrefix(g, "flagd flags.json "), g)
	}
	require.Equal(t, []string{"a"}, sink.entries()[1].(events.ConfigurationChange).Updated)
}
