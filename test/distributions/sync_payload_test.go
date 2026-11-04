package distributions

import (
	"bytes"
	"crypto/sha256"
	byteorder "encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var updateSyncPayload = flag.Bool("update-sync-payload", false, "rewrite testdata/flag_sync_payload.golden from the current flagd-full")

const syncPayloadGolden = "testdata/flag_sync_payload.golden"

const syncService = "/flagd.sync.v1.FlagSyncService/"

// startFlagdFull starts the flagd-full the Builder makes with the given sources and returns its sync port.
func startFlagdFull(t *testing.T, sources ...string) (syncPort int) {
	t.Helper()
	ports := startDistribution(t, "flagd-full", []string{"port", "management-port", "sync-port", "ofrep-port"}, nil, sources...)
	return ports["sync-port"]
}

// startDistribution starts the distribution of distributions/<name> the Builder makes, with the given sources and extra
// arguments, on a free port for each of the port flags named. It returns the ports, waits for readiness and stops the
// process with the test.
func startDistribution(t *testing.T, name string, portFlags, extra []string, sources ...string) map[string]int {
	t.Helper()
	path := filepath.Join(root(t), "distributions", name, "manifest.yaml")
	b := build(t, path)
	require.NoError(t, b.err, b.out)

	ports := map[string]int{}
	for _, flagName := range portFlags {
		ports[flagName] = freePort(t)
	}
	args := []string{"start"}
	for _, source := range sources {
		args = append(args, "--uri", "file:"+source)
	}
	for flagName, port := range ports {
		args = append(args, "--"+flagName, fmt.Sprint(port))
	}
	args = append(args, extra...)
	cmd := exec.Command(binary(t, path), args...)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
		}
	})

	ready := fmt.Sprintf("http://127.0.0.1:%d/readyz", ports["management-port"])
	require.Eventually(t, func() bool {
		resp, err := http.Get(ready) //nolint:noctx // a local probe
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 60*time.Second, 200*time.Millisecond, "%s becomes ready: %s", name, &stderr)
	return ports
}

// canonical renders the envelope of a Connect response with sorted keys and its numbers as written, so that the
// formatting protojson chooses for each response does not show. The flag configuration inside it is a string and stays
// exactly as the endpoint sent it.
func canonical(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	require.NoError(t, dec.Decode(&v), string(raw))
	out, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	return string(out)
}

// headersOf renders the response headers a client sees, without the ones that change from run to run.
func headersOf(h http.Header) string {
	var lines []string
	for name, values := range h {
		if name == "Date" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", name, strings.Join(values, ", ")))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func post(t *testing.T, url, contentType string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, out
}

// firstStreamMessage sends a SyncFlags request in the Connect streaming JSON encoding and returns the first message of
// the response; the stream stays open after it.
func firstStreamMessage(t *testing.T, base, request string) []byte {
	t.Helper()
	envelope := make([]byte, 5, 5+len(request))
	byteorder.BigEndian.PutUint32(envelope[1:], uint32(len(request)))
	envelope = append(envelope, request...)
	req, err := http.NewRequest(http.MethodPost, base+syncService+"SyncFlags", bytes.NewReader(envelope))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/connect+json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	prefix := make([]byte, 5)
	_, err = io.ReadFull(resp.Body, prefix)
	require.NoError(t, err)
	require.Zero(t, prefix[0], "a message frame, not the end of the stream")
	message := make([]byte, byteorder.BigEndian.Uint32(prefix[1:]))
	_, err = io.ReadFull(resp.Body, message)
	require.NoError(t, err)
	return message
}

// TestFlagSyncPayloadCharacterization pins what flagd-full's flag sync endpoint sends for two file sources with flag
// sets and metadata: the first message of SyncFlags with and without a selector, FetchAllFlags, GetMetadata, and the
// body, ETag and headers of the flags route. A distribution that serves flag sync from the same sources (UC-017) keeps
// every byte of it.
func TestFlagSyncPayloadCharacterization(t *testing.T) {
	dir, paths := writeSyncSources(t)
	port := startFlagdFull(t, paths...)
	got := flagSyncTranscript(t, fmt.Sprintf("http://127.0.0.1:%d", port), dir)
	if *updateSyncPayload {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(syncPayloadGolden, []byte(got), 0o600))
		return
	}
	require.Equal(t, recordedSyncPayload(t), got)
}

// recordedSyncPayload is what flagd-full's flag sync endpoint sent when the golden was recorded.
func recordedSyncPayload(t *testing.T) string {
	t.Helper()
	want, err := os.ReadFile(syncPayloadGolden)
	require.NoError(t, err, "record the current flagd-full with `go test -run TestFlagSyncPayloadCharacterization -update-sync-payload`")
	return string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")))
}

// writeSyncSources writes the two file sources of the flag sync characterization, with flag sets and metadata, and
// returns their directory and paths.
func writeSyncSources(t *testing.T) (dir string, paths []string) {
	t.Helper()
	dir = t.TempDir()
	sources := map[string]string{
		"a.json": `{"metadata":{"flagSetId":"set-a","owner":"team-a"},"flags":{
  "flag-a": {"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false},"metadata":{"purpose":"a"}},
  "shared": {"state":"ENABLED","defaultVariant":"red","variants":{"red":"red","blue":"blue"}}}}`,
		"b.json": `{"metadata":{"flagSetId":"set-b","owner":"team-b"},"flags":{
  "flag-b": {"state":"DISABLED","defaultVariant":"zero","variants":{"zero":0,"one":1}}}}`,
	}
	for _, name := range []string{"a.json", "b.json"} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(sources[name]), 0o600))
		paths = append(paths, path)
	}
	return dir, paths
}

// flagSyncTranscript sends the requests of the characterization to a flag sync endpoint and renders what came back,
// with the directory of the sources masked.
func flagSyncTranscript(t *testing.T, base, dir string) string {
	t.Helper()
	var out strings.Builder
	section := func(title, body string) { fmt.Fprintf(&out, "== %s\n%s\n", title, body) }

	section("SyncFlags, first message, no selector", canonical(t, firstStreamMessage(t, base, `{}`)))
	section("SyncFlags, first message, selector flagSetId=set-a", canonical(t, firstStreamMessage(t, base, `{"selector":"flagSetId=set-a"}`)))

	resp, body := post(t, base+syncService+"FetchAllFlags", "application/json", []byte(`{}`))
	section("FetchAllFlags", fmt.Sprintf("%d\n%s\n%s", resp.StatusCode, headersOf(resp.Header), canonical(t, body)))
	resp, body = post(t, base+syncService+"FetchAllFlags", "application/json", []byte(`{"selector":"flagSetId=set-b"}`))
	section("FetchAllFlags, selector flagSetId=set-b", fmt.Sprintf("%d\n%s", resp.StatusCode, canonical(t, body)))
	resp, body = post(t, base+syncService+"GetMetadata", "application/json", []byte(`{}`))
	section("GetMetadata", fmt.Sprintf("%d\n%s\n%s", resp.StatusCode, headersOf(resp.Header), canonical(t, body)))

	for _, selector := range []string{"", "flagSetId=set-a"} {
		req, err := http.NewRequest(http.MethodGet, base+"/v1/flags", nil)
		require.NoError(t, err)
		if selector != "" {
			req.Header.Set("Flagd-Selector", selector)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		// the body holds the path of each source, so its length and its ETag depend on the temporary directory
		sum := sha256.Sum256(body)
		require.Equal(t, `"`+hex.EncodeToString(sum[:])+`"`, resp.Header.Get("ETag"), "the ETag is the SHA-256 of the body")
		require.Equal(t, fmt.Sprint(len(body)), resp.Header.Get("Content-Length"))
		require.NotEmpty(t, resp.Header.Get("Last-Modified"))
		for _, name := range []string{"Etag", "Content-Length", "Last-Modified"} {
			resp.Header.Set(name, "<"+strings.ToLower(name)+">")
		}
		section(fmt.Sprintf("GET /v1/flags, selector %q", selector),
			fmt.Sprintf("%d\n%s\n%s", resp.StatusCode, headersOf(resp.Header), body))
	}

	return strings.ReplaceAll(out.String(), dir, "<dir>")
}
