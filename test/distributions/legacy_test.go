package distributions

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"

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
	client := &http.Client{}
	body, contentType := `{"flagKey":"bool-flag"}`, "application/json"
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
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(data)
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

// TestFlagdFullServesDeprecatedServices is the other side: flagd-full lists the endpoint, so the single calls answer.
func TestFlagdFullServesDeprecatedServices(t *testing.T) {
	port := startDistribution(t, "flagd-full", allPortFlags, nil, writeParityFlags(t))["port"]
	for _, path := range []string{"/schema.v1.Service/ResolveBoolean", "/flagd.evaluation.v1.Service/ResolveBoolean", "/flagd.evaluation.v2.Service/ResolveBoolean"} {
		status, body := callAt(t, port, path, false)
		require.Equal(t, http.StatusOK, status, path)
		require.Contains(t, body, `"value":true`, path)
	}
}

// TestComponentsListsTheHostOfTheLegacyEndpoint pins UC-011 and UC-010 BR-008: flagd components names the endpoint
// that serves the deprecated services in flagd-full and flagd-testbed, and lists it in no other standard distribution.
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
