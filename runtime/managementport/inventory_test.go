//go:build unix

package managementport

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	evaluationV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	"buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/sync/v1/syncv1connect"
	evalV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	syncv1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// callEveryEndpoint makes one call of each kind the inventory covers: ConnectRPC single and bulk, OFREP single,
// bulk and a 304, flag sync's SyncFlags, FetchAllFlags and its flags route.
func callEveryEndpoint(t *testing.T, servicePort, ofrepPort, syncPort uint16) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	eval := evaluationV1.NewServiceClient(http.DefaultClient, fmt.Sprintf("http://127.0.0.1:%d", servicePort))
	_, err := eval.ResolveBoolean(ctx, connect.NewRequest(&evalV1.ResolveBooleanRequest{FlagKey: "f1"}))
	require.NoError(t, err)
	_, err = eval.ResolveAll(ctx, connect.NewRequest(&evalV1.ResolveAllRequest{}))
	require.NoError(t, err)

	ofrep := fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/evaluate/flags", ofrepPort)
	post := func(url, etag string) *http.Response {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{"context":{}}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		return resp
	}
	require.Equal(t, http.StatusOK, post(ofrep+"/f1", "").StatusCode)
	bulk := post(ofrep, "")
	require.Equal(t, http.StatusOK, bulk.StatusCode)
	require.NotEmpty(t, bulk.Header.Get("ETag"))
	require.Equal(t, http.StatusNotModified, post(ofrep, bulk.Header.Get("ETag")).StatusCode)

	syncURL := fmt.Sprintf("http://127.0.0.1:%d", syncPort)
	sync := syncv1connect.NewFlagSyncServiceClient(http.DefaultClient, syncURL)
	_, err = sync.FetchAllFlags(ctx, connect.NewRequest(&syncv1.FetchAllFlagsRequest{}))
	require.NoError(t, err)
	streamCtx, stop := context.WithCancel(ctx)
	stream, err := sync.SyncFlags(streamCtx, connect.NewRequest(&syncv1.SyncFlagsRequest{}))
	require.NoError(t, err)
	require.True(t, stream.Receive(), "SyncFlags delivers the flags: %v", stream.Err())
	stop()
	_ = stream.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, syncURL+"/v1/flags", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// the sync stream's end is recorded when the server notices the cancelled stream
	time.Sleep(200 * time.Millisecond)
}

// excludedFamilies are the Go runtime, process and exporter metrics, which flagd does not define.
var excludedFamilies = regexp.MustCompile(`^(go|process|promhttp)_`)

// volatileLabels change from run to run or machine to machine; their values are masked.
var volatileLabels = map[string]bool{
	"host_name": true, "host_arch": true, "host_id": true,
	"os_description": true, "os_type": true, "os_version": true,
	"process_runtime_description": true, "process_runtime_name": true, "process_runtime_version": true,
	"process_pid": true, "process_executable_name": true, "process_executable_path": true,
	"process_owner": true, "process_command_args": true,
	"service_instance_id": true, "telemetry_sdk_version": true,
	"server_address": true, "server_port": true,
}

var sample = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(.*)\})?\s`)
var label = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"`)

// inventory renders the metric families flagd defines as sorted lines: each family's TYPE, then each series with
// its labels, the values of volatile labels masked and the sample values dropped, so that names, types, label keys,
// bounded label values and histogram buckets are pinned. The file and the remote source are replaced by
// placeholders.
func inventory(exposition, file, remote string) string {
	var lines []string
	for _, line := range strings.Split(exposition, "\n") {
		if name, ok := strings.CutPrefix(line, "# TYPE "); ok {
			if !excludedFamilies.MatchString(name) {
				lines = append(lines, "# TYPE "+name)
			}
			continue
		}
		m := sample.FindStringSubmatch(line)
		if m == nil || excludedFamilies.MatchString(m[1]) {
			continue
		}
		var labels []string
		for _, l := range label.FindAllStringSubmatch(m[2], -1) {
			value := l[2]
			if volatileLabels[l[1]] || m[1] == "target_info" && l[1] != "service_name" {
				value = "*"
			}
			value = strings.ReplaceAll(value, file, "<file>")
			value = strings.ReplaceAll(value, remote, "<remote>")
			labels = append(labels, l[1]+"="+value)
		}
		slices.Sort(labels)
		lines = append(lines, m[1]+"{"+strings.Join(labels, ",")+"}")
	}
	slices.Sort(lines)
	return strings.Join(slices.Compact(lines), "\n") + "\n"
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run go test -update to create %s", path)
	require.Equal(t, string(want), got)
}
