//go:build unix

package runtime

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	syncv1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/sync/v1/syncv1connect"
	syncpb "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"connectrpc.com/connect"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

var updateEndpointSettings = flag.Bool("update-endpoint-settings", false,
	"rewrite testdata/endpoint_settings.golden from the current behaviour")

const endpointSettingsGolden = "testdata/endpoint_settings.golden"

// The effective settings of the official endpoints are pinned through what the endpoints do, not through Config: a
// flag of `flagd start` is a setting of flagd-full only if an endpoint honours it. The Builder (UC-001 Q15) changes how
// settings reach the endpoints and keeps every line of this golden.
const endpointFlags = `{"flags":{
  "ctx-flag": {"state":"ENABLED","defaultVariant":"no","variants":{"yes":"yes","no":"no"},
    "targeting":{"if":[{"and":[{"==":[{"var":"tier"},"gold"]},{"==":[{"var":"env"},"prod"]}]},"yes","no"]}}}}`

type response struct {
	status int
	header http.Header
	body   string
	err    error
}

func do(client *http.Client, method, url, body string, header map[string]string) response {
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		return response{err: err}
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return response{err: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: string(raw)}
}

// describe reduces a response to the parts a setting decides.
func describe(r response, headers ...string) string {
	if r.err != nil {
		return "no response"
	}
	parts := []string{fmt.Sprintf("status %d", r.status)}
	for _, h := range headers {
		if v := r.header.Get(h); v != "" {
			parts = append(parts, fmt.Sprintf("%s=%q", h, v))
		}
	}
	return strings.Join(parts, ", ")
}

// jsonField returns a field of a JSON body, compact.
func jsonField(body, field string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return "not JSON"
	}
	raw, ok := m[field]
	if !ok {
		return "absent"
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return string(raw)
	}
	return out.String()
}

func normalize(s string, p ports) string {
	for name, port := range map[string]uint16{"MANAGEMENT": p.management, "SERVICE": p.service, "OFREP": p.ofrep, "SYNC": p.sync} {
		s = strings.ReplaceAll(s, fmt.Sprintf("%d", port), "<"+name+">")
	}
	return s
}

func writeSelfSigned(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "flagd-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certPath, keyPath
}

func endpointConfig(t *testing.T, p ports) Config {
	t.Helper()
	cfg := characterizedConfig(t, p)
	file := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, os.WriteFile(file, []byte(endpointFlags), 0o600))
	cfg.SyncProviders = []coresync.SourceConfig{{URI: file, Provider: "file"}}
	return cfg
}

func TestEndpointSettingsCharacterization(t *testing.T) {
	var out strings.Builder
	section := func(name string) { fmt.Fprintf(&out, "== %s\n", name) }
	line := func(p ports, format string, args ...any) {
		fmt.Fprintln(&out, normalize(fmt.Sprintf(format, args...), p))
	}

	// Every setting at a non-default value, over plain HTTP.
	t.Run("configured", func(t *testing.T) {
		p := newPorts(t)
		cfg := endpointConfig(t, p)
		cfg.CORS = []string{"https://app.example"}
		cfg.OfrepSSEEnabled = true
		cfg.OfrepSSEInactivityDel = 77
		cfg.OfrepSSEPublicURL = "https://relay.example"
		cfg.MaxRequestBodyBytes = 200
		cfg.MaxRequestHeaderBytes = 1500
		cfg.ContextValues = map[string]any{"tier": "gold"}
		cfg.HeaderToContextKeyMappings = map[string]string{"X-Env": "env"}
		cfg.SyncServiceHTTPEnabled = true
		startRuntime(t, cfg)

		client := http.DefaultClient
		svc := fmt.Sprintf("http://127.0.0.1:%d", p.service)
		ofrep := fmt.Sprintf("http://127.0.0.1:%d", p.ofrep)
		syncURL := fmt.Sprintf("http://127.0.0.1:%d", p.sync)
		single := `{"flagKey":"ctx-flag"}`
		section("configured")

		// ports: each endpoint answers on its own
		line(p, "connect on the service port, no header: %s",
			jsonField(do(client, http.MethodPost, svc+"/flagd.evaluation.v1.Service/ResolveString", single, nil).body, "value"))
		line(p, "ofrep on the ofrep port, no header: %s",
			jsonField(do(client, http.MethodPost, ofrep+"/ofrep/v1/evaluate/flags/ctx-flag", `{}`, nil).body, "value"))
		line(p, "flag sync over HTTP on the sync port: %s",
			describe(do(client, http.MethodGet, syncURL+"/v1/flags", "", nil), "Content-Type"))

		// context values and header mappings reach the evaluation context
		line(p, "connect, header X-Env: prod: %s",
			jsonField(do(client, http.MethodPost, svc+"/flagd.evaluation.v1.Service/ResolveString", single, map[string]string{"X-Env": "prod"}).body, "value"))
		line(p, "ofrep, header X-Env: prod: %s",
			jsonField(do(client, http.MethodPost, ofrep+"/ofrep/v1/evaluate/flags/ctx-flag", `{}`, map[string]string{"X-Env": "prod"}).body, "value"))
		line(p, "ofrep, header X-Env: dev: %s",
			jsonField(do(client, http.MethodPost, ofrep+"/ofrep/v1/evaluate/flags/ctx-flag", `{}`, map[string]string{"X-Env": "dev"}).body, "value"))

		// CORS on every endpoint
		for _, e := range []struct{ name, url string }{
			{"connect", svc + "/flagd.evaluation.v1.Service/ResolveString"},
			{"ofrep", ofrep + "/ofrep/v1/evaluate/flags/ctx-flag"},
			{"flag sync", syncURL + "/v1/flags"},
		} {
			for _, origin := range []string{"https://app.example", "https://other.example"} {
				r := do(client, http.MethodOptions, e.url, "", map[string]string{
					"Origin": origin, "Access-Control-Request-Method": "POST",
				})
				line(p, "CORS preflight %s from %s: %s", e.name, origin,
					describe(r, "Access-Control-Allow-Origin", "Access-Control-Allow-Methods"))
			}
		}

		// OFREP's event stream advertisement
		bulk := do(client, http.MethodPost, ofrep+"/ofrep/v1/evaluate/flags", `{}`, nil)
		line(p, "ofrep bulk: %s, eventStreams %s", describe(bulk), jsonField(bulk.body, "eventStreams"))

		// request size limits
		big := `{"context":{"padding":"` + strings.Repeat("x", 400) + `"}}`
		line(p, "ofrep body over the limit: %s", describe(do(client, http.MethodPost, ofrep+"/ofrep/v1/evaluate/flags/ctx-flag", big, nil)))
		line(p, "connect body over the limit: %s",
			describe(do(client, http.MethodPost, svc+"/flagd.evaluation.v1.Service/ResolveString",
				`{"flagKey":"ctx-flag","context":{"padding":"`+strings.Repeat("x", 400)+`"}}`, nil)))
		line(p, "ofrep header over the limit: %s",
			describe(do(client, http.MethodPost, ofrep+"/ofrep/v1/evaluate/flags/ctx-flag", `{}`,
				map[string]string{"X-Padding": strings.Repeat("h", 3000)})))
	})

	// Defaults of the options that change what is served: SSE off, flag sync over HTTP off.
	t.Run("sse and sync over http off", func(t *testing.T) {
		p := newPorts(t)
		cfg := endpointConfig(t, p)
		cfg.OfrepSSEEnabled = false
		cfg.SyncServiceHTTPEnabled = false
		cfg.DisableSyncMetadata = true
		startRuntime(t, cfg)

		client := http.DefaultClient
		ofrep := fmt.Sprintf("http://127.0.0.1:%d", p.ofrep)
		syncURL := fmt.Sprintf("http://127.0.0.1:%d", p.sync)
		section("sse and sync over http off")
		bulk := do(client, http.MethodPost, ofrep+"/ofrep/v1/evaluate/flags", `{}`, nil)
		line(p, "ofrep bulk: %s, eventStreams %s", describe(bulk), jsonField(bulk.body, "eventStreams"))
		line(p, "ofrep SSE endpoint: %s", describe(do(client, http.MethodGet, ofrep+"/ofrep/v1/sse/x", "", nil)))
		line(p, "flag sync over HTTP: %s", describe(do(client, http.MethodGet, syncURL+"/v1/flags", "", nil)))
		meta := do(client, http.MethodPost, syncURL+"/flagd.sync.v1.FlagSyncService/GetMetadata", `{}`, nil)
		line(p, "sync GetMetadata disabled: %s", describe(meta))
	})

	// TLS on the service and sync endpoints, and the server-side stream deadline.
	t.Run("tls and stream deadline", func(t *testing.T) {
		p := newPorts(t)
		cert, key := writeSelfSigned(t)
		cfg := endpointConfig(t, p)
		cfg.ServiceCertPath, cfg.ServiceKeyPath = cert, key
		cfg.StreamDeadline = 700 * time.Millisecond
		cfg.SyncServiceHTTPEnabled = true
		startRuntime(t, cfg)

		tlsClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // self-signed test certificate
		section("tls and stream deadline")
		https := do(tlsClient, http.MethodPost, fmt.Sprintf("https://127.0.0.1:%d/flagd.evaluation.v1.Service/ResolveString", p.service), `{"flagKey":"ctx-flag"}`, nil)
		line(p, "connect over https: %s %s", describe(https), jsonField(https.body, "value"))
		plain := do(http.DefaultClient, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/flagd.evaluation.v1.Service/ResolveString", p.service), `{"flagKey":"ctx-flag"}`, nil)
		line(p, "connect over plain http: %s", describe(plain))
		syncTLS := do(tlsClient, http.MethodGet, fmt.Sprintf("https://127.0.0.1:%d/v1/flags", p.sync), "", nil)
		line(p, "flag sync over https: %s", describe(syncTLS))
		ofrep := do(http.DefaultClient, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/evaluate/flags/ctx-flag", p.ofrep), `{}`, nil)
		line(p, "ofrep stays plain http: %s", describe(ofrep))

		client := syncv1.NewFlagSyncServiceClient(tlsClient, fmt.Sprintf("https://127.0.0.1:%d", p.sync))
		stream, err := client.SyncFlags(context.Background(), connect.NewRequest(&syncpb.SyncFlagsRequest{}))
		require.NoError(t, err)
		began := time.Now()
		received := 0
		for stream.Receive() {
			received++
		}
		ended := time.Since(began)
		line(p, "flag sync stream: %d message(s), ended by the deadline: %v, error code: %v",
			received, ended > 400*time.Millisecond && ended < 3*time.Second, connect.CodeOf(stream.Err()))
	})

	if *updateEndpointSettings {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(endpointSettingsGolden, []byte(out.String()), 0o600))
		return
	}
	want, err := os.ReadFile(endpointSettingsGolden)
	require.NoError(t, err, "record the current behaviour with `go test -run TestEndpointSettingsCharacterization -update-endpoint-settings`")
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if got := out.String(); got != string(want) {
		wantLines, gotLines := strings.Split(string(want), "\n"), strings.Split(got, "\n")
		for n := 0; n < len(wantLines) && n < len(gotLines); n++ {
			if wantLines[n] != gotLines[n] {
				t.Fatalf("endpoint settings changed at line %d\nwant: %s\n got: %s", n+1, wantLines[n], gotLines[n])
			}
		}
		t.Fatalf("endpoint settings changed: %d lines recorded, %d produced", len(wantLines), len(gotLines))
	}
}
