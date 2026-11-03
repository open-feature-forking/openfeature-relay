//go:build unix

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	serviceexample "github.com/open-feature/flagd/test/service-example"
	"github.com/stretchr/testify/require"
)

// TestRuntimeStartsExtensionEndpoint pins that the runtime needs no knowledge of the type of a service endpoint to
// start it and stop it: the example endpoint of UC-004, which lives in a module of its own and imports the core
// module only, is built from its factory, resolves a flag through the router, announces a change of the file
// source, and stops with the runtime.
func TestRuntimeStartsExtensionEndpoint(t *testing.T) {
	p := newPorts(t)
	cfg := characterizedConfig(t, p)
	source := cfg.SyncProviders[0].URI
	extensionPort := int(freePort(t))
	cfg.EndpointSettings = map[string]map[string]any{
		serviceexample.Type: {
			"port":    extensionPort,
			"formats": []string{"flagd"},
			"sources": []string{source},
		},
	}

	rt, err := FromConfig(logger.NewLogger(nil, false), "test", cfg, Components{
		Endpoints: []service.Factory{serviceexample.Factory{}},
	})
	require.NoError(t, err)
	require.Len(t, rt.Endpoints, 1)
	require.Equal(t, serviceexample.Type, rt.Endpoints[0].Type)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.start(ctx) }()
	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", p.management)) //nolint:noctx // local
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 20*time.Second, 50*time.Millisecond)

	base := fmt.Sprintf("http://127.0.0.1:%d", extensionPort)
	get := func(path, ifNoneMatch string) (*http.Response, map[string]any) {
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		require.NoError(t, err)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", `"`+ifNoneMatch+`"`)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp, body
	}

	resp, flag := get("/v0/flags/f1", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, true, flag["value"], "the flag of the file source, through the router")

	_, changes := get("/v0/changes?wait=0s", "")
	known, _ := changes["validator"].(string)
	require.NotEmpty(t, known)

	announced := make(chan string, 1)
	go func() {
		_, body := get("/v0/changes?wait=10s", known)
		validator, _ := body["validator"].(string)
		announced <- validator
	}()
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, os.WriteFile(source, []byte(`{"flags":{"f2":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true}}}}`), 0o600))
	select {
	case validator := <-announced:
		require.NotEmpty(t, validator)
		require.NotEqual(t, known, validator, "the endpoint announces the change of the file source")
	case <-time.After(10 * time.Second):
		t.Fatal("the extension endpoint announced no change")
	}

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "the runtime stops the extension endpoint with the others")
	case <-time.After(20 * time.Second):
		t.Fatal("start did not return")
	}
}
