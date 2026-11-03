//go:build unix

// Package distribution_test runs flagd-full with the example event sink (UC-020 main scenario, step 6 E5a). It is a
// module of its own, so that the example module needs the core only. It builds one runtime and stops it with
// SIGTERM, which Start handles itself.
package distribution_test

import (
	"context"
	"encoding/json"
	"fmt"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	eventsinkexample "github.com/open-feature/flagd/test/eventsink-example"
	"github.com/stretchr/testify/require"
)

const (
	flagsBefore = `{"flags":{"f1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`
	flagsAfter  = `{"flags":{"f1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}},` +
		`"f2":{"state":"ENABLED","defaultVariant":"off","variants":{"on":true,"off":false}}}}`
)

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

type payload struct {
	Kind    string   `json:"kind"`
	FlagKey string   `json:"flagKey"`
	Variant string   `json:"variant"`
	Added   []string `json:"added"`
}

// receiver is the webhook the example sink posts its batches to.
type receiver struct {
	mu       sync.Mutex
	received []payload
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var batch []payload
	if err := json.NewDecoder(req.Body).Decode(&batch); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	r.received = append(r.received, batch...)
	r.mu.Unlock()
}

func (r *receiver) all() []payload {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.received)
}

// TestExtensionEventSink builds flagd-full's components with the example event sink, makes one OFREP evaluation and
// changes the flag file. With a flush interval that never elapses, the sink's receiver stays empty until the runtime
// shuts down, and then gets the evaluation and the configuration change that added a flag.
func TestExtensionEventSink(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "flags.json")
	require.NoError(t, os.WriteFile(source, []byte(flagsBefore), 0o600))
	rcv := &receiver{}
	webhook := httptest.NewServer(rcv)
	t.Cleanup(webhook.Close)

	components := flagdcomponents.Official()
	components.EventSinks = []eventsink.Configured{{
		Factory: eventsinkexample.Factory{},
		Config: map[string]any{
			eventsinkexample.SettingURL: webhook.URL,
			// the age of a batch would not flush it within the test, so only the shutdown delivers
			eventsink.KeyFlushInterval: "1h",
		},
	}}
	ofrepPort := freePort(t)
	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), "test", runtime.Config{
		ManagementPort:               freePort(t),
		ServicePort:                  freePort(t),
		OfrepServicePort:             ofrepPort,
		SyncServicePort:              freePort(t),
		StreamDeadline:               5 * time.Second,
		KeepAliveMinTime:             runtime.KeepAliveMinTimeDefault,
		KeepAlivePermitWithoutStream: runtime.KeepAlivePermitWithoutStreamDefault,
		SyncProviders:                []coresync.SourceConfig{{URI: source, Provider: "file"}},
	}, components)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- rt.Start() }()

	evaluate := func(key string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		url := fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/evaluate/flags/%s", ofrepPort, key)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{"context":{}}`))
		if err != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	require.Eventually(t, func() bool { return evaluate("f1") }, 20*time.Second, 50*time.Millisecond,
		"OFREP evaluates f1")

	require.NoError(t, os.WriteFile(source, []byte(flagsAfter), 0o600))
	require.Eventually(t, func() bool { return evaluate("f2") }, 20*time.Second, 50*time.Millisecond,
		"the file change adds f2")

	require.Never(t, func() bool { return len(rcv.all()) > 0 }, 500*time.Millisecond, 50*time.Millisecond,
		"the sink holds its events until the runtime shuts down")

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("the runtime did not stop")
	}

	received := rcv.all()
	require.True(t, slices.ContainsFunc(received, func(p payload) bool {
		return p.Kind == string(events.KindEvaluation) && p.FlagKey == "f1" && p.Variant == "on"
	}), "the evaluation of f1 reaches the receiver: %+v", received)
	require.True(t, slices.ContainsFunc(received, func(p payload) bool {
		return p.Kind == string(events.KindConfigurationChange) && slices.Equal(p.Added, []string{"f2"})
	}), "the configuration change that adds f2 reaches the receiver: %+v", received)
}
