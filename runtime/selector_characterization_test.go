//go:build unix

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/sync/v1/syncv1connect"
	syncv1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"connectrpc.com/connect"
	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	flageval "github.com/open-feature-forking/openfeature-relay/service/flag-evaluation"
	"github.com/open-feature-forking/openfeature-relay/service/ofrep"
	flagsync "github.com/open-feature-forking/openfeature-relay/service/flag-sync"
	"github.com/stretchr/testify/require"
)

// selectorSettings builds the settings of an official endpoint over a store of two sources: src1, flag set set1,
// holds the flag a; src2, flag set set2, holds the flag b.
func selectorSettings(t *testing.T, factory service.Factory) service.Settings {
	t.Helper()
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"src1", "src2"})
	require.NoError(t, err)
	format := flagdformat.NewFormat(st, log)
	for source, flag := range map[string]string{"src1": "a", "src2": "b"} {
		_, err = format.Accept(context.Background(), configformat.Input{
			Source: source,
			Data: fmt.Sprintf(`{"metadata":{"flagSetId":"set%s"},"flags":{%q:{"state":"ENABLED","defaultVariant":"on",`+
				`"variants":{"on":true,"off":false}}}}`, source[3:], flag),
		})
		require.NoError(t, err)
	}
	inputs, err := newEndpointInputs(log, st, events.NewBus(), []service.Factory{factory}, []string{"flagd"}, flagdcomponents.Official().Evaluators, nil, false)
	require.NoError(t, err)
	settings := inputs.settings(factory)
	initialized := make(chan struct{})
	close(initialized)
	settings.Initialized = initialized
	return settings
}

// exchange sends a request with the selector header and returns the status, the response headers and the body.
func exchange(t *testing.T, method, url, selector string, body string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if selector != "" {
		req.Header.Set("Flagd-Selector", selector)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, resp.Header, out
}

// flagKeys returns the sorted keys of the flags of a document, found under the first of paths that holds them.
func flagKeys(t *testing.T, doc []byte, flagsField string) []string {
	t.Helper()
	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc, &decoded), string(doc))
	var keys []string
	var asMap map[string]json.RawMessage
	var asList []struct {
		Key string `json:"key"`
	}
	switch raw := decoded[flagsField]; {
	case json.Unmarshal(raw, &asMap) == nil:
		for key := range asMap {
			keys = append(keys, key)
		}
	case json.Unmarshal(raw, &asList) == nil:
		for _, f := range asList {
			keys = append(keys, f.Key)
		}
	}
	sort.Strings(keys)
	return keys
}

// TestSelectorCharacterization pins where each official endpoint takes its selector from and what it does with
// it, without a bound selector: the Flagd-Selector header, flag sync's request field against the header, OFREP's
// ETag and event stream channel, and the answer to an invalid selector.
func TestSelectorCharacterization(t *testing.T) {
	selectors := []struct {
		name, selector string
		keys           []string
	}{
		{"none", "", []string{"a", "b"}},
		{"source", "source=src1", []string{"a"}},
		{"flag set", "flagSetId=set2", []string{"b"}},
		{"bare source", "src2", []string{"b"}},
		{"unknown source", "source=nowhere", nil},
	}

	t.Run("flag evaluation", func(t *testing.T) {
		settings := selectorSettings(t, flageval.Factory{})
		port := freePort(t)
		settings.Config = map[string]any{flageval.ConfigKey: flageval.EndpointConfig{
			Configuration: service.Configuration{Port: port, ServiceName: "flagd"},
		}}
		serveFactory(t, flageval.Factory{}, settings)
		waitForPort(t, port)
		base := fmt.Sprintf("http://127.0.0.1:%d/flagd.evaluation.v1.Service/", port)

		for _, tt := range selectors {
			t.Run(tt.name, func(t *testing.T) {
				status, _, body := exchange(t, http.MethodPost, base+"ResolveAll", tt.selector, `{}`)
				require.Equal(t, http.StatusOK, status, string(body))
				require.Equal(t, tt.keys, flagKeys(t, body, "flags"))

				for _, key := range []string{"a", "b"} {
					status, _, body = exchange(t, http.MethodPost, base+"ResolveBoolean", tt.selector, fmt.Sprintf(`{"flagKey":%q}`, key))
					if slices.Contains(tt.keys, key) {
						require.Equal(t, http.StatusOK, status, string(body))
					} else {
						require.Equal(t, http.StatusNotFound, status, "a flag outside the selector is not found: "+string(body))
					}
				}
			})
		}

		t.Run("invalid selector", func(t *testing.T) {
			for _, rpc := range []struct{ name, body string }{{"ResolveAll", `{}`}, {"ResolveBoolean", `{"flagKey":"a"}`}} {
				status, _, body := exchange(t, http.MethodPost, base+rpc.name, "colour=red", rpc.body)
				require.Equal(t, http.StatusBadRequest, status, string(body))
				require.Contains(t, string(body), "invalid_argument")
				require.Contains(t, string(body), `invalid selector key \"colour\"`)
			}
		})
	})

	t.Run("OFREP", func(t *testing.T) {
		settings := selectorSettings(t, ofrep.Factory{})
		port := freePort(t)
		settings.Config = map[string]any{ofrep.ConfigKey: ofrep.EndpointConfig{
			Service: ofrep.SvcConfiguration{Port: port, ServiceName: "flagd", SSEEnabled: true, SSEInactivityDelaySec: 60},
			Formats: []string{flagdformat.Type},
		}}
		serveFactory(t, ofrep.Factory{}, settings)
		waitForPort(t, port)
		base := fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/", port)

		for _, tt := range selectors {
			t.Run(tt.name, func(t *testing.T) {
				status, _, body := exchange(t, http.MethodPost, base+"evaluate/flags", tt.selector, `{}`)
				require.Equal(t, http.StatusOK, status, string(body))
				require.Equal(t, tt.keys, flagKeys(t, body, "flags"))

				for _, key := range []string{"a", "b"} {
					status, _, body = exchange(t, http.MethodPost, base+"evaluate/flags/"+key, tt.selector, `{}`)
					if slices.Contains(tt.keys, key) {
						require.Equal(t, http.StatusOK, status, string(body))
					} else {
						require.Equal(t, http.StatusNotFound, status, "a flag outside the selector is not found: "+string(body))
					}
				}
			})
		}

		t.Run("invalid selector", func(t *testing.T) {
			status, _, body := exchange(t, http.MethodPost, base+"evaluate/flags", "colour=red", `{}`)
			require.Equal(t, http.StatusBadRequest, status)
			require.Contains(t, string(body), "invalid selector: invalid selector key")
			status, _, body = exchange(t, http.MethodPost, base+"evaluate/flags/a", "colour=red", `{}`)
			require.Equal(t, http.StatusBadRequest, status)
			require.Contains(t, string(body), "invalid selector: invalid selector key")
			status, _, _ = exchange(t, http.MethodGet, base+"sse/colour=red", "", "")
			require.Equal(t, http.StatusBadRequest, status, "the event stream refuses an invalid channel")
		})

		t.Run("ETag", func(t *testing.T) {
			status, header1, _ := exchange(t, http.MethodPost, base+"evaluate/flags", "source=src1", `{}`)
			require.Equal(t, http.StatusOK, status)
			etag1 := header1.Get("ETag")
			require.NotEmpty(t, etag1)
			_, header2, _ := exchange(t, http.MethodPost, base+"evaluate/flags", "source=src2", `{}`)
			require.NotEqual(t, etag1, header2.Get("ETag"), "the response is tagged, so another selector gets another tag")

			req, err := http.NewRequest(http.MethodPost, base+"evaluate/flags", strings.NewReader(`{}`))
			require.NoError(t, err)
			req.Header.Set("Flagd-Selector", "source=src1")
			req.Header.Set("If-None-Match", etag1)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			resp.Body.Close()
			require.Equal(t, http.StatusNotModified, resp.StatusCode)
		})

		t.Run("event stream channel", func(t *testing.T) {
			lastModified := func(selector string) (any, []string) {
				_, _, body := exchange(t, http.MethodPost, base+"evaluate/flags", selector, `{}`)
				var decoded struct {
					Metadata     map[string]any `json:"metadata"`
					EventStreams []struct {
						Endpoint struct {
							RequestURI string `json:"requestUri"`
						} `json:"endpoint"`
					} `json:"eventStreams"`
				}
				require.NoError(t, json.Unmarshal(body, &decoded), string(body))
				var uris []string
				for _, s := range decoded.EventStreams {
					uris = append(uris, s.Endpoint.RequestURI)
				}
				return decoded.Metadata["flagConfigLastModified"], uris
			}

			value, uris := lastModified("source=src1")
			require.Nil(t, value, "no stream is live for the selector")
			require.Equal(t, []string{"/ofrep/v1/sse/" + url.PathEscape("source=src1")}, uris,
				"the bulk response advertises the client's own expression as the channel")

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"sse/source=src1", nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { resp.Body.Close() })
			require.Equal(t, http.StatusOK, resp.StatusCode)

			require.Eventually(t, func() bool {
				v, _ := lastModified("source=src1")
				return v != nil
			}, 5*time.Second, 25*time.Millisecond, "a live stream for the same expression dates the bulk response")
			v, _ := lastModified("src1")
			require.Nil(t, v, "the channel is the expression as sent: the same selector in another spelling has no stream")
			v, _ = lastModified("source=src2")
			require.Nil(t, v)
		})
	})

	t.Run("flag sync", func(t *testing.T) {
		settings := selectorSettings(t, flagsync.Factory{})
		port := freePort(t)
		settings.Config = map[string]any{flagsync.ConfigKey: flagsync.SvcConfigurations{
			Port: port, ServiceName: "flagd", HTTPEnabled: true,
		}}
		serveFactory(t, flagsync.Factory{}, settings)
		waitForPort(t, port)
		baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
		client := syncv1connect.NewFlagSyncServiceClient(http.DefaultClient, baseURL)

		fetch := func(header, body string) ([]string, error) {
			req := connect.NewRequest(&syncv1.FetchAllFlagsRequest{Selector: body}) //nolint:staticcheck // the request field is what is pinned
			if header != "" {
				req.Header().Set("Flagd-Selector", header)
			}
			resp, err := client.FetchAllFlags(context.Background(), req)
			if err != nil {
				return nil, err
			}
			return flagKeys(t, []byte(resp.Msg.GetFlagConfiguration()), "flags"), nil
		}
		syncFirst := func(header, body string) ([]string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req := connect.NewRequest(&syncv1.SyncFlagsRequest{Selector: body}) //nolint:staticcheck // the request field is what is pinned
			if header != "" {
				req.Header().Set("Flagd-Selector", header)
			}
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
		httpRoute := func(header string) ([]string, int) {
			status, _, body := exchange(t, http.MethodGet, baseURL+"/v1/flags", header, "")
			if status != http.StatusOK {
				return nil, status
			}
			return flagKeys(t, bytes.TrimSpace(body), "flags"), status
		}

		for _, tt := range selectors {
			t.Run(tt.name, func(t *testing.T) {
				got, err := fetch(tt.selector, "")
				require.NoError(t, err)
				require.Equal(t, tt.keys, got, "FetchAllFlags, selector in the header")
				got, err = fetch("", tt.selector)
				require.NoError(t, err)
				require.Equal(t, tt.keys, got, "FetchAllFlags, selector in the request field")
				got, err = syncFirst(tt.selector, "")
				require.NoError(t, err)
				require.Equal(t, tt.keys, got, "SyncFlags, selector in the header")
				got, err = syncFirst("", tt.selector)
				require.NoError(t, err)
				require.Equal(t, tt.keys, got, "SyncFlags, selector in the request field")
				got, status := httpRoute(tt.selector)
				require.Equal(t, http.StatusOK, status)
				require.Equal(t, tt.keys, got, "the HTTP route, selector in the header")
			})
		}

		t.Run("the header wins over the request field", func(t *testing.T) {
			got, err := fetch("source=src1", "source=src2")
			require.NoError(t, err)
			require.Equal(t, []string{"a"}, got)
			got, err = syncFirst("source=src1", "source=src2")
			require.NoError(t, err)
			require.Equal(t, []string{"a"}, got)
		})

		t.Run("invalid selector", func(t *testing.T) {
			for name, call := range map[string]func(header, body string) ([]string, error){"FetchAllFlags": fetch, "SyncFlags": syncFirst} {
				for _, expression := range []string{"colour=red", "source=\x01"} {
					if !strings.ContainsRune(expression, 1) { // a header cannot carry a control character
						_, err := call(expression, "")
						require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), name+" "+expression)
					}
					_, err := call("", expression)
					require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), name+" "+expression)
				}
			}
			_, status := httpRoute("colour=red")
			require.Equal(t, http.StatusBadRequest, status)
		})
	})
}
