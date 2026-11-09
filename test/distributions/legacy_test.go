package distributions

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// deprecatedCalls are the calls of the deprecated flag evaluation services that old providers make.
var deprecatedCalls = []string{
	"/schema.v1.Service/ResolveBoolean", "/schema.v1.Service/ResolveAll", "/schema.v1.Service/EventStream",
	"/flagd.evaluation.v1.Service/ResolveBoolean", "/flagd.evaluation.v1.Service/ResolveAll", "/flagd.evaluation.v1.Service/EventStream",
}

// callAt sends a JSON request over HTTP/1.1, or a gRPC-framed one over cleartext HTTP/2, and returns the status.
func callAt(t *testing.T, port int, path string, grpc bool) (int, string) {
	t.Helper()
	return callWith(t, port, path, grpc, `{"flagKey":"bool-flag"}`)
}

// callWith is callAt with the JSON body of the request. The client times out, so a call that regresses into one that
// never answers fails the test instead of hanging it.
func callWith(t *testing.T, port int, path string, grpc bool, jsonBody string) (int, string) {
	t.Helper()
	resp := postTo(t, port, path, grpc, jsonBody)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err, path)
	return resp.StatusCode, string(data)
}

// openStream opens a gRPC stream and returns the status and content type of its response headers, without reading the
// stream, which does not end.
func openStream(t *testing.T, port int, path string) (int, string) {
	t.Helper()
	resp := postTo(t, port, path, true, "")
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Content-Type")
}

func postTo(t *testing.T, port int, path string, grpc bool, jsonBody string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	body, contentType := jsonBody, "application/json"
	if grpc {
		var protocols http.Protocols
		protocols.SetUnencryptedHTTP2(true)
		client.Transport = &http.Transport{Protocols: &protocols}
		body, contentType = "\x00\x00\x00\x00\x00", "application/grpc"
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", contentType)
	resp, err := client.Do(req)
	require.NoError(t, err, path)
	return resp
}

// TestSlimDistributionsDoNotServeDeprecatedServices pins UC-010 A7 and BR-008: the distributions that do not list the
// deprecated evaluation endpoint answer 404 over HTTP, which gRPC clients read as Unimplemented, on every call of
// schema.v1 and flagd.evaluation.v1, and keep serving evaluation v2.
func TestSlimDistributionsDoNotServeDeprecatedServices(t *testing.T) {
	flags := writeParityFlags(t)
	for _, d := range standard {
		if !d.evaluates {
			continue
		}
		t.Run(d.name, func(t *testing.T) {
			port := startDistribution(t, d.name, d.portFlags, nil, flags)["port"]
			for _, path := range deprecatedCalls {
				for _, grpc := range []bool{false, true} {
					status, body := callAt(t, port, path, grpc)
					require.Equal(t, http.StatusNotFound, status, "%s (grpc %t)", path, grpc)
					require.Equal(t, "404 page not found\n", body, path)
				}
			}
			status, body := callAt(t, port, "/flagd.evaluation.v2.Service/ResolveBoolean", false)
			require.Equal(t, http.StatusOK, status)
			require.Contains(t, body, `"value":true`)
		})
	}
}

// TestFlagdFullServesDeprecatedServices is the other side: flagd-full lists the endpoint, so ResolveBoolean and
// ResolveAll answer on both deprecated services and their EventStream opens.
func TestFlagdFullServesDeprecatedServices(t *testing.T) {
	port := startDistribution(t, "flagd-full", allPortFlags, nil, writeParityFlags(t))["port"]
	for _, service := range []string{"schema.v1", "flagd.evaluation.v1", "flagd.evaluation.v2"} {
		prefix := "/" + service + ".Service/"
		status, body := callAt(t, port, prefix+"ResolveBoolean", false)
		require.Equal(t, http.StatusOK, status, prefix+"ResolveBoolean")
		require.Contains(t, body, `"value":true`, prefix+"ResolveBoolean")
		if service == "flagd.evaluation.v2" {
			continue
		}
		status, body = callWith(t, port, prefix+"ResolveAll", false, `{}`)
		require.Equal(t, http.StatusOK, status, prefix+"ResolveAll")
		require.Contains(t, body, "bool-flag", prefix+"ResolveAll")
		status, contentType := openStream(t, port, prefix+"EventStream")
		require.Equal(t, http.StatusOK, status, prefix+"EventStream")
		require.Contains(t, contentType, "application/grpc", prefix+"EventStream")
	}
}

// TestComponentsListsTheHostOfTheLegacyEndpoint pins UC-011 and UC-010 BR-008: flagd components names the endpoint
// that serves the deprecated services in flagd-full, and lists it in no other standard distribution.
func TestComponentsListsTheHostOfTheLegacyEndpoint(t *testing.T) {
	endpoints := func(name string) map[string]string {
		path := manifestOf(t, name)
		b := build(t, path)
		require.NoError(t, b.err, b.out)
		out, err := exec.Command(binary(t, path), "components", "--output", "json").Output()
		require.NoError(t, err)
		var listing struct {
			Components map[string][]struct {
				Type string `json:"type"`
				Host string `json:"host"`
			} `json:"components"`
		}
		require.NoError(t, json.Unmarshal(out, &listing))
		types := map[string]string{}
		for _, c := range listing.Components["service_endpoints"] {
			types[c.Type] = c.Host
		}
		return types
	}

	full := endpoints("flagd-full")
	require.Equal(t, "flag-evaluation", full["flag-evaluation-legacy"])
	require.Empty(t, full["flag-evaluation"], "the host itself has no host")
	for _, d := range standard {
		require.NotContains(t, endpoints(d.name), "flag-evaluation-legacy", d.name)
	}
}
