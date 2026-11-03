//go:build unix

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/sync/v1/syncv1connect"
	syncv1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"connectrpc.com/connect"
	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	flageval "github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation"
	"github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation/ofrep"
	flagsync "github.com/open-feature/flagd/flagd/pkg/service/flag-sync"
	"github.com/stretchr/testify/require"
)

const boundHeader = "X-Bound"

// bindFromHeader is a middleware that binds the selector named by the X-Bound request header, if there is one.
func bindFromHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bound := r.Header.Get(boundHeader); bound != "" {
			r = r.WithContext(middleware.BindSelector(r.Context(), bound))
		}
		next.ServeHTTP(w, r)
	})
}

// exchangeBound is exchange for a request bound to a selector.
func exchangeBound(t *testing.T, method, url, selector, bound, body string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if selector != "" {
		req.Header.Set("Flagd-Selector", selector)
	}
	if bound != "" {
		req.Header.Set(boundHeader, bound)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, resp.Header, out
}

// TestBoundSelectorNarrowing pins, on every official endpoint, that a request bound to a selector by a middleware
// is resolved for the effective selector: the bound selector narrowed by the client's. A client's selector that
// names another value of the same key than the bound one gets no flags, and FLAG_NOT_FOUND for a single flag
// without evaluating it. An invalid bound selector is an error, never an unrestricted request.
func TestBoundSelectorNarrowing(t *testing.T) {
	rows := []struct {
		name, bound, client string
		keys                []string
	}{
		{"bound only", "flagSetId=set1", "", []string{"a"}},
		{"bound source only", "source=src2", "", []string{"b"}},
		{"the client narrows with another key", "flagSetId=set1", "source=src1", []string{"a"}},
		{"the client narrows to nothing", "flagSetId=set1", "source=src2", nil},
		{"the client repeats the bound value", "flagSetId=set2", "flagSetId=set2", []string{"b"}},
		{"conflict", "flagSetId=set1", "flagSetId=set2", nil},
		{"a bare client selector is a source", "source=src2", "src1", nil},
		{"a bare client selector repeats the bound source", "source=src2", "src2", []string{"b"}},
	}
	const invalidBound = "colour=red"

	t.Run("flag evaluation", func(t *testing.T) {
		settings := selectorSettings(t, flageval.Factory{})
		settings.HTTPMiddleware = bindFromHeader
		port := freePort(t)
		settings.Config = map[string]any{flageval.ConfigKey: flageval.EndpointConfig{
			Configuration: service.Configuration{Port: port, ServiceName: "flagd"},
		}}
		serveFactory(t, flageval.Factory{}, settings)
		waitForPort(t, port)
		base := fmt.Sprintf("http://127.0.0.1:%d/flagd.evaluation.v1.Service/", port)

		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				status, _, body := exchangeBound(t, http.MethodPost, base+"ResolveAll", row.client, row.bound, `{}`)
				require.Equal(t, http.StatusOK, status, string(body))
				require.Equal(t, row.keys, flagKeys(t, body, "flags"))

				for _, key := range []string{"a", "b"} {
					status, _, body = exchangeBound(t, http.MethodPost, base+"ResolveBoolean", row.client, row.bound,
						fmt.Sprintf(`{"flagKey":%q}`, key))
					if slices.Contains(row.keys, key) {
						require.Equal(t, http.StatusOK, status, string(body))
					} else {
						require.Equal(t, http.StatusNotFound, status, "flag "+key+": "+string(body))
					}
				}
			})
		}

		t.Run("invalid bound selector", func(t *testing.T) {
			for _, rpc := range []struct{ name, body string }{{"ResolveAll", `{}`}, {"ResolveBoolean", `{"flagKey":"a"}`}} {
				status, _, body := exchangeBound(t, http.MethodPost, base+rpc.name, "", invalidBound, rpc.body)
				require.Equal(t, http.StatusBadRequest, status, string(body))
				require.Contains(t, string(body), "invalid bound selector")
			}
		})
	})

	t.Run("OFREP", func(t *testing.T) {
		settings := selectorSettings(t, ofrep.Factory{})
		settings.HTTPMiddleware = bindFromHeader
		port := freePort(t)
		settings.Config = map[string]any{ofrep.ConfigKey: ofrep.EndpointConfig{
			Service: ofrep.SvcConfiguration{Port: port, ServiceName: "flagd", SSEEnabled: true, SSEInactivityDelaySec: 60},
			Formats: []string{flagdformat.Type},
		}}
		serveFactory(t, ofrep.Factory{}, settings)
		waitForPort(t, port)
		base := fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/", port)

		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				status, _, body := exchangeBound(t, http.MethodPost, base+"evaluate/flags", row.client, row.bound, `{}`)
				require.Equal(t, http.StatusOK, status, string(body))
				require.Equal(t, row.keys, flagKeys(t, body, "flags"))

				for _, key := range []string{"a", "b"} {
					status, _, body = exchangeBound(t, http.MethodPost, base+"evaluate/flags/"+key, row.client, row.bound, `{}`)
					if slices.Contains(row.keys, key) {
						require.Equal(t, http.StatusOK, status, string(body))
					} else {
						require.Equal(t, http.StatusNotFound, status, "flag "+key+": "+string(body))
						require.Contains(t, string(body), "FLAG_NOT_FOUND")
					}
				}
			})
		}

		t.Run("invalid bound selector", func(t *testing.T) {
			for _, path := range []string{"evaluate/flags", "evaluate/flags/a"} {
				status, _, body := exchangeBound(t, http.MethodPost, base+path, "", invalidBound, `{}`)
				require.Equal(t, http.StatusBadRequest, status, string(body))
				require.Contains(t, string(body), "invalid bound selector")
			}
			status, _, _ := exchangeBound(t, http.MethodGet, base+"sse", "", invalidBound, "")
			require.Equal(t, http.StatusBadRequest, status, "the event stream refuses it too")
		})

		t.Run("event stream channel", func(t *testing.T) {
			dated := func(selector, bound string) bool {
				_, _, body := exchangeBound(t, http.MethodPost, base+"evaluate/flags", selector, bound, `{}`)
				var decoded struct {
					Metadata map[string]any `json:"metadata"`
				}
				require.NoError(t, json.Unmarshal(body, &decoded), string(body))
				return decoded.Metadata["flagConfigLastModified"] != nil
			}
			stream := func(channel, bound string) {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"sse/"+channel, nil)
				require.NoError(t, err)
				req.Header.Set(boundHeader, bound)
				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				t.Cleanup(func() { resp.Body.Close() })
				require.Equal(t, http.StatusOK, resp.StatusCode)
			}

			require.False(t, dated("source=src1", "flagSetId=set1"))
			stream("source=src1", "flagSetId=set1")
			require.Eventually(t, func() bool { return dated("source=src1", "flagSetId=set1") },
				5*time.Second, 25*time.Millisecond, "a stream bound the same way dates the response")
			require.False(t, dated("source=src1", "flagSetId=set2"), "a request bound another way has no stream of its own")
			require.False(t, dated("source=src1", ""), "an unbound request has no stream of its own")
		})
	})

	t.Run("flag sync", func(t *testing.T) {
		settings := selectorSettings(t, flagsync.Factory{})
		settings.HTTPMiddleware = bindFromHeader
		port := freePort(t)
		settings.Config = map[string]any{flagsync.ConfigKey: flagsync.SvcConfigurations{
			Port: port, ServiceName: "flagd", HTTPEnabled: true,
		}}
		serveFactory(t, flagsync.Factory{}, settings)
		waitForPort(t, port)
		baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
		client := syncv1connect.NewFlagSyncServiceClient(http.DefaultClient, baseURL)

		fetch := func(header, bound string) ([]string, error) {
			req := connect.NewRequest(&syncv1.FetchAllFlagsRequest{})
			req.Header().Set("Flagd-Selector", header)
			req.Header().Set(boundHeader, bound)
			resp, err := client.FetchAllFlags(context.Background(), req)
			if err != nil {
				return nil, err
			}
			return flagKeys(t, []byte(resp.Msg.GetFlagConfiguration()), "flags"), nil
		}
		syncFirst := func(header, bound string) ([]string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req := connect.NewRequest(&syncv1.SyncFlagsRequest{})
			req.Header().Set("Flagd-Selector", header)
			req.Header().Set(boundHeader, bound)
			stream, err := client.SyncFlags(ctx, req)
			if err != nil {
				return nil, err
			}
			defer stream.Close()
			if !stream.Receive() {
				return nil, stream.Err()
			}
			return flagKeys(t, []byte(stream.Msg().GetFlagConfiguration()), "flags"), nil
		}

		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				got, err := fetch(row.client, row.bound)
				require.NoError(t, err)
				require.Equal(t, row.keys, got, "FetchAllFlags")
				got, err = syncFirst(row.client, row.bound)
				require.NoError(t, err)
				require.Equal(t, row.keys, got, "SyncFlags")

				status, _, body := exchangeBound(t, http.MethodGet, baseURL+"/v1/flags", row.client, row.bound, "")
				require.Equal(t, http.StatusOK, status, string(body))
				require.Equal(t, row.keys, flagKeys(t, body, "flags"), "the HTTP route")
			})
		}

		t.Run("invalid bound selector", func(t *testing.T) {
			_, err := fetch("", invalidBound)
			require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
			_, err = syncFirst("", invalidBound)
			require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
			status, _, _ := exchangeBound(t, http.MethodGet, baseURL+"/v1/flags", "", invalidBound, "")
			require.Equal(t, http.StatusBadRequest, status)
		})
	})
}
