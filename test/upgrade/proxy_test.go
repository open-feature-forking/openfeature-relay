package upgrade

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// flagd-proxy is not rebuilt on the runtime (UC-017 BR-007), but it syncs through this repository's sync providers, so
// it shows #78's sync items too. This scenario runs the flagd-proxy built from the baseline commit and the one built
// from this tree on the same file source and the same subscriber, and compares what the subscriber and the management
// port show.

// The ports flagd-proxy listens on by default; the harness publishes them as the ports of flagd's sync and OFREP.
const (
	proxyPortSync       = portSync
	proxyPortManagement = portOFREP
)

// grpcResponse is a gRPC call's answer: the messages it sent, and the status it ended with.
type grpcResponse struct {
	messages [][]byte
	status   string
	message  string
}

// grpcClient speaks gRPC over HTTP/2 without TLS (prior knowledge), as a flagd provider does to flagd-proxy.
func grpcClient() *http.Client {
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Client{Transport: &http.Transport{Protocols: protocols}}
}

// stringField encodes a protobuf message whose only set field is a string.
func stringField(number int, value string) []byte {
	if value == "" {
		return nil
	}
	out := binary.AppendUvarint(nil, uint64(number<<3|2))
	out = binary.AppendUvarint(out, uint64(len(value)))
	return append(out, value...)
}

// grpcCall sends a request message and collects the messages the server sends for the time given.
func (r *run) grpcCall(path string, message []byte, within time.Duration) (grpcResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	body := append([]byte{0, 0, 0, 0, 0}, message...)
	binary.BigEndian.PutUint32(body[1:5], uint32(len(message)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+r.ports[proxyPortSync]+path, bytes.NewReader(body))
	if err != nil {
		return grpcResponse{}, err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	resp, err := grpcClient().Do(req)
	if err != nil {
		return grpcResponse{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body) // ends at the deadline for a stream, with what arrived until then

	out := grpcResponse{status: resp.Trailer.Get("Grpc-Status"), message: resp.Trailer.Get("Grpc-Message")}
	if out.status == "" { // a call that fails at once answers with the status in the headers
		out.status, out.message = resp.Header.Get("Grpc-Status"), resp.Header.Get("Grpc-Message")
	}
	for len(data) >= 5 {
		n := int(binary.BigEndian.Uint32(data[1:5]))
		if len(data) < 5+n {
			break
		}
		out.messages = append(out.messages, data[5:5+n])
		data = data[5+n:]
	}
	return out, nil
}

// flagConfiguration returns field 1 of a sync response, the flag configuration, normalized; the other fields are named
// when present.
func flagConfiguration(message []byte) string {
	var config, others []string
	for len(message) > 0 {
		tag, n := binary.Uvarint(message)
		if n <= 0 {
			return "<undecodable>"
		}
		message = message[n:]
		if tag&7 != 2 {
			return "<unexpected wire type>"
		}
		length, n := binary.Uvarint(message)
		if n <= 0 || uint64(len(message)-n) < length {
			return "<undecodable>"
		}
		value := message[n : n+int(length)]
		message = message[n+int(length):]
		if tag>>3 == 1 {
			config = append(config, JSON(value))
		} else {
			others = append(others, fmt.Sprintf("field%d", tag>>3))
		}
	}
	return strings.Join(append(config, others...), " ")
}

func (g grpcResponse) rendered() string {
	var lines []string
	for _, m := range g.messages {
		lines = append(lines, flagConfiguration(m))
	}
	lines = append(lines, fmt.Sprintf("grpc-status=%s %s", g.status, Text(g.message)))
	return strings.Join(lines, "\n")
}

// activeStreams returns the sample lines of the sync_active_streams series.
func (r *run) activeStreams() string {
	_, _, body, err := r.do(http.MethodGet, proxyPortManagement, "/metrics", "", nil)
	if err != nil {
		return "<metrics unavailable>"
	}
	var lines []string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "sync_active_streams") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return strings.Join(lines, "\n")
}

// proxyReady waits for the management port to answer /healthz.
func (r *run) proxyReady(within time.Duration) bool {
	r.t.Helper()
	r.mapPorts()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if status, _, err := r.get(proxyPortManagement, "/healthz", nil); err == nil && status == http.StatusOK {
			return true
		}
	}
	return false
}

var proxyScenarioMu sync.Mutex

// scenarioFlagdProxy is Q20 of step 10: one file source and one subscriber against flagd-proxy. It covers the first
// delivery, a change of the file, a deleted file, a source that does not exist and one that cannot be reached, and
// the metric of the open streams.
func scenarioFlagdProxy(t *testing.T, flagdImage string) Transcript {
	proxyScenarioMu.Lock()
	defer proxyScenarioMu.Unlock()
	image := proxyImages(t, flagdImage)
	r := start(t, image, []string{"start", "--log-format", "json"}, nil, map[string]string{"flags.json": flagsJSON})
	require.True(t, r.proxyReady(60*time.Second), "%s did not become ready: %s", image, r.logs())

	var tr Transcript
	for _, path := range []string{"/healthz", "/readyz"} {
		status, _, err := r.get(proxyPortManagement, path, nil)
		require.NoError(t, err)
		tr.add("probe"+path, fmt.Sprint(status))
	}

	const service = "/flagd.sync.v1.FlagSyncService/"
	source := "file:/data/flags.json"
	fetch, err := r.grpcCall(service+"FetchAllFlags", stringField(2, source), 20*time.Second)
	require.NoError(t, err)
	tr.add("fetch-all/file", fetch.rendered())

	was := `"bool-flag":{"state":"ENABLED","variants":{"on":true,"off":false},"defaultVariant":"on"}`
	changed := strings.Replace(flagsJSON, was, strings.Replace(was, `"defaultVariant":"on"`, `"defaultVariant":"off"`, 1), 1)
	require.NotEqual(t, flagsJSON, changed)
	flagFile := filepath.Join(r.dir, "flags.json")

	// the subscriber stays connected while the file changes and is deleted
	metric := make(chan string, 1)
	go func() {
		time.Sleep(2 * time.Second)
		metric <- r.activeStreams()
		time.Sleep(time.Second)
		_ = os.WriteFile(flagFile, []byte(changed), 0o644)
		time.Sleep(6 * time.Second)
		_ = os.Remove(flagFile)
	}()
	stream, err := r.grpcCall(service+"SyncFlags", stringField(2, source), 18*time.Second)
	require.NoError(t, err)
	// the stream ends at the deadline of the call, which is no status of the server
	stream.status, stream.message = "<open>", ""
	tr.add("sync-flags/file", stream.rendered())
	tr.add("sync-active-streams/during", <-metric)

	missing, err := r.grpcCall(service+"FetchAllFlags", stringField(2, "file:/data/missing.json"), 20*time.Second)
	require.NoError(t, err)
	tr.add("fetch-all/missing-file", missing.rendered())
	unreachable, err := r.grpcCall(service+"FetchAllFlags", stringField(2, "http://127.0.0.1:1/flags.json"), 20*time.Second)
	require.NoError(t, err)
	tr.add("fetch-all/unreachable-source", unreachable.rendered())

	tr.add("sync-active-streams/after", r.activeStreams())
	status, _, err := r.get(proxyPortManagement, "/healthz", nil)
	require.NoError(t, err)
	tr.add("probe/healthz-after", fmt.Sprint(status))
	return tr
}
