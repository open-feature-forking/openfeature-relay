//go:build unix

package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/stretchr/testify/require"
)

// extensionFactory is a service endpoint the runtime knows nothing of, as an Extension Author's module would
// provide it: it resolves a flag through the injected resolver and tells a client that waits when a configuration
// change event arrives. The full example is test/service-example, which may not import the runtime.
type extensionFactory struct{}

func (extensionFactory) Type() string         { return "extension-api" }
func (extensionFactory) Needs() service.Needs { return service.Needs{Router: true} }

func (extensionFactory) Create(s service.Settings) (service.Endpoint, error) {
	port, ok := s.Config["port"].(int)
	if !ok {
		return nil, errors.New("the extension endpoint needs a port")
	}
	return extensionEndpoint{settings: s, port: port}, nil
}

type extensionEndpoint struct {
	settings service.Settings
	port     int
}

func (e extensionEndpoint) Serve(ctx context.Context) error {
	listener, err := e.settings.Listen.Or()("tcp", ":"+strconv.Itoa(e.port))
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/flags/{key}", func(w http.ResponseWriter, r *http.Request) {
		value, _, _, _, err := e.settings.Resolver.ResolveBooleanValue(r.Context(), "", r.PathValue("key"), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		fmt.Fprint(w, value)
	})
	mux.HandleFunc("/changes", func(w http.ResponseWriter, r *http.Request) {
		sub := e.settings.Bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindConfigurationChange}, Buffer: 4})
		defer sub.Close()
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case change := <-sub.C():
			fmt.Fprint(w, change.(events.ConfigurationChange).Source)
		case <-r.Context().Done():
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// TestRuntimeStartsExtensionEndpoint pins that the runtime needs no knowledge of the type of a service endpoint to
// start it and stop it: an endpoint of a type the runtime has never seen is built from its factory, resolves a flag
// through the router, learns of a change of the file source from the event bus, and stops with the runtime.
func TestRuntimeStartsExtensionEndpoint(t *testing.T) {
	p := newPorts(t)
	cfg := characterizedConfig(t, p)
	source := cfg.SyncProviders[0].URI
	extensionPort := int(freePort(t))
	cfg.EndpointSettings = map[string]map[string]any{extensionFactory{}.Type(): {"port": extensionPort}}

	rt, err := FromConfig(logger.NewLogger(nil, false), "test", cfg, listening(withFlagd(Components{Endpoints: []service.Factory{extensionFactory{}}})))
	require.NoError(t, err)
	require.Len(t, rt.Endpoints, 1)
	require.Equal(t, "extension-api", rt.Endpoints[0].Type)

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
	resp, err := http.Get(base + "/flags/f1") //nolint:noctx // local
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "the flag of the file source, through the router")

	announced := make(chan string, 1)
	go func() {
		resp, err := http.Get(base + "/changes") //nolint:noctx // local
		if err != nil {
			announced <- err.Error()
			return
		}
		defer resp.Body.Close()
		buf := make([]byte, 256)
		n, _ := resp.Body.Read(buf)
		announced <- string(buf[:n])
	}()
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, os.WriteFile(source, []byte(`{"flags":{"f2":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true}}}}`), 0o600))
	select {
	case got := <-announced:
		require.Equal(t, source, got, "the endpoint learns of the change of the file source from the bus")
	case <-time.After(10 * time.Second):
		t.Fatal("the extension endpoint heard of no change")
	}

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "the runtime stops the extension endpoint with the others")
	case <-time.After(20 * time.Second):
		t.Fatal("start did not return")
	}
}
