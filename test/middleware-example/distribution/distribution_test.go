//go:build unix

// Package distribution_test runs flagd-full's service endpoints behind the example middleware (UC-005 main
// scenario, step 6 M5a). It is a module of its own, so that the example module needs the core only. It
// builds one runtime and stops it with SIGTERM, which Start handles itself.
package distribution_test

import (
	"context"
	"encoding/json"
	"fmt"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	middlewareexample "github.com/open-feature/flagd/test/middleware-example"
	"github.com/stretchr/testify/require"
)

const flags = `{"flags":{
	"a1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false},"metadata":{"flagSetId":"team-a"}},
	"a2":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false},"metadata":{"flagSetId":"team-a"}},
	"b1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false},"metadata":{"flagSetId":"team-b"}}
}}`

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

// TestExtensionMiddlewareBindsSelector builds flagd-full's components with the example middleware and evaluates all
// flags over OFREP: a key bound to team-a's flag set sees only team-a's flags, a key bound to no selector sees all of
// them, and a request without a known key is answered with 401 before it reaches the service endpoint.
func TestExtensionMiddlewareBindsSelector(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "flags.json")
	require.NoError(t, os.WriteFile(source, []byte(flags), 0o600))

	components := flagdcomponents.Components()
	components.Middleware = []middleware.Configured{{
		Factory: middlewareexample.Factory{},
		Config: map[string]any{middlewareexample.SettingKeys: map[string]any{
			"key-for-team-a": "flagSetId=team-a",
			"key-for-all":    "",
		}},
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
	t.Cleanup(func() {
		require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(20 * time.Second):
			t.Error("the runtime did not stop")
		}
	})

	url := fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/evaluate/flags", ofrepPort)
	bulk := func(key string) (int, []string) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{"context":{}}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set(middlewareexample.DefaultHeader, key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, nil
		}
		defer resp.Body.Close()
		var body struct {
			Flags []struct {
				Key string `json:"key"`
			} `json:"flags"`
		}
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, nil
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		keys := make([]string, 0, len(body.Flags))
		for _, f := range body.Flags {
			keys = append(keys, f.Key)
		}
		slices.Sort(keys)
		return resp.StatusCode, keys
	}

	require.Eventually(t, func() bool {
		status, keys := bulk("key-for-all")
		return status == http.StatusOK && len(keys) == 3
	}, 20*time.Second, 50*time.Millisecond, "OFREP serves the three flags")

	status, keys := bulk("key-for-team-a")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []string{"a1", "a2"}, keys, "the key's selector narrows the bulk evaluation")

	status, keys = bulk("key-for-all")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []string{"a1", "a2", "b1"}, keys)

	status, _ = bulk("")
	require.Equal(t, http.StatusUnauthorized, status, "no key")
	status, _ = bulk("unknown-key")
	require.Equal(t, http.StatusUnauthorized, status, "an unknown key")
}
