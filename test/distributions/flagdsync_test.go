package distributions

import (
	"bytes"
	byteorder "encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file runs the configuration proxy, flagd-sync, as built binaries (UC-017). The tests that compare it with
// flagd-full are in parity_test.go (the byte-for-byte replay of the flag sync characterization, and the invalid flag
// configuration).

var syncPortFlags = []string{"management-port", "sync-port"}

// process is a distribution binary the test started.
type process struct {
	cmd  *exec.Cmd
	done chan struct{}
	out  *lockedBuffer
}

// launch starts the binary with an empty home directory. The process is stopped with the test.
func launch(t *testing.T, bin string, args ...string) *process {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
	p := &process{cmd: cmd, done: make(chan struct{}), out: &lockedBuffer{}}
	cmd.Stdout, cmd.Stderr = p.out, p.out
	require.NoError(t, cmd.Start())
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() { p.stop(20 * time.Second) })
	return p
}

// stop sends SIGTERM and waits for the exit, then kills the process if it does not come.
func (p *process) stop(within time.Duration) {
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(within):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// settle waits until the process has logged something and then nothing more for a while, which is when a source it
// rejects has been read and reported.
func (p *process) settle(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	last, since := -1, time.Now()
	for time.Now().Before(deadline) {
		if n := len(p.out.String()); n != last {
			last, since = n, time.Now()
		} else if last > 0 && time.Since(since) > 1500*time.Millisecond && strings.Contains(p.out.String(), `"level":"`) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the process did not settle: %s", p.out.String())
}

// waitLogged waits until the process has logged the text.
func (p *process) waitLogged(t *testing.T, text string, within time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool { return strings.Contains(p.out.String(), text) }, within, 100*time.Millisecond,
		"the process logs %q: %s", text, p.out.String())
}

func (p *process) running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// probe is the client of the readiness check; a port that accepts and never answers must not hold a wait past its
// deadline.
var probe = &http.Client{Timeout: 2 * time.Second}

func isReady(managementPort int) bool {
	resp, err := probe.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", managementPort)) //nolint:noctx // a local probe
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func waitReady(t *testing.T, p *process, managementPort int) {
	t.Helper()
	require.Eventually(t, func() bool { return isReady(managementPort) }, 60*time.Second, 200*time.Millisecond,
		"the distribution becomes ready: %s", p.out.String())
}

func portArgs(ports map[string]int) []string {
	var args []string
	for name, port := range ports {
		args = append(args, "--"+name, fmt.Sprint(port))
	}
	return args
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func flagSyncBase(ports map[string]int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", ports["sync-port"])
}

// syncStream is an open SyncFlags subscription: the messages arrive on a channel until the stream ends.
type syncStream struct {
	msgs chan string
	body io.Closer
}

// openSyncStream subscribes with the request in the Connect streaming JSON encoding and the headers.
func openSyncStream(t *testing.T, base, request string, header map[string]string) *syncStream {
	t.Helper()
	envelope := make([]byte, 5, 5+len(request))
	byteorder.BigEndian.PutUint32(envelope[1:], uint32(len(request)))
	envelope = append(envelope, request...)
	req, err := http.NewRequest(http.MethodPost, base+syncService+"SyncFlags", bytes.NewReader(envelope))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/connect+json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	s := &syncStream{msgs: make(chan string, 16), body: resp.Body}
	t.Cleanup(func() { _ = resp.Body.Close() })
	go func() {
		defer close(s.msgs)
		for {
			prefix := make([]byte, 5)
			if _, err := io.ReadFull(resp.Body, prefix); err != nil {
				return
			}
			message := make([]byte, byteorder.BigEndian.Uint32(prefix[1:]))
			if _, err := io.ReadFull(resp.Body, message); err != nil {
				return
			}
			if prefix[0] != 0 {
				return // the end-of-stream frame
			}
			var m struct {
				FlagConfiguration string `json:"flagConfiguration"`
			}
			if json.Unmarshal(message, &m) != nil {
				return
			}
			s.msgs <- m.FlagConfiguration
		}
	}()
	return s
}

// next returns the next flag configuration of the stream, and false if none arrives in time or the stream ended.
func (s *syncStream) next(within time.Duration) (string, bool) {
	select {
	case m, ok := <-s.msgs:
		return m, ok
	case <-time.After(within):
		return "", false
	}
}

// fetchAll asks the flag sync endpoint for the flag configuration, with the headers.
func fetchAll(t *testing.T, base string, header map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+syncService+"FetchAllFlags", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var m struct {
		FlagConfiguration string `json:"flagConfiguration"`
		Message           string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(body, &m), string(body))
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, m.Message
	}
	return resp.StatusCode, m.FlagConfiguration
}

// TestFlagSyncProxyServesInProcessProvider pins UC-017's main scenario on the built flagd-sync: it is ready once every
// source delivered, a provider that subscribes with a selector gets the flags that match it, and a change of a source
// reaches it.
func TestFlagSyncProxyServesInProcessProvider(t *testing.T) {
	dir, paths := writeSyncSources(t)
	ports := startDistribution(t, "flagd-sync", syncPortFlags, nil, paths...)
	base := flagSyncBase(ports)

	stream := openSyncStream(t, base, `{"selector":"flagSetId=set-a"}`, nil)
	first, ok := stream.next(10 * time.Second)
	require.True(t, ok, "the first flag configuration")
	require.Contains(t, first, "flag-a")
	require.Contains(t, first, "shared")
	require.NotContains(t, first, "flag-b", "only the flags the selector names")

	status, all := fetchAll(t, base, nil)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, all, "flag-b", "without a selector, every source")

	writeFile(t, filepath.Join(dir, "a.json"), `{"metadata":{"flagSetId":"set-a","owner":"team-a"},"flags":{
  "flag-a": {"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}},
  "flag-new": {"state":"ENABLED","defaultVariant":"off","variants":{"on":true,"off":false}}}}`)
	deadline := time.Now().Add(30 * time.Second)
	for {
		changed, ok := stream.next(time.Until(deadline))
		require.True(t, ok, "the changed flag configuration reaches the subscriber")
		if strings.Contains(changed, "flag-new") {
			require.NotContains(t, changed, "flag-b")
			break
		}
	}

	writeFile(t, filepath.Join(dir, "b.json"), `{"metadata":{"flagSetId":"set-b","owner":"team-b"},"flags":{
  "flag-b": {"state":"ENABLED","defaultVariant":"one","variants":{"zero":0,"one":1}}}}`)
	require.Eventually(t, func() bool {
		_, body := fetchAll(t, base, nil)
		return strings.Contains(body, `"defaultVariant":"one"`)
	}, 30*time.Second, 200*time.Millisecond, "the change of b.json is taken in")
	unexpected, ok := stream.next(3 * time.Second)
	require.False(t, ok, "a change of set-b reaches no subscriber of set-a: %s", unexpected)
}

// listeningPorts returns the TCP ports the process listens on, read from the sockets it holds open: no other process
// on the machine can change the answer.
func listeningPorts(t *testing.T, pid int) []int {
	t.Helper()
	inodes := map[string]bool{}
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	require.NoError(t, err)
	for _, fd := range fds {
		target, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, fd.Name()))
		if err == nil && strings.HasPrefix(target, "socket:[") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = true
		}
	}
	var ports []int
	for _, table := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile("/proc/net/" + table)
		require.NoError(t, err)
		for _, line := range strings.Split(string(data), "\n")[1:] {
			fields := strings.Fields(line)
			// local_address is host:port in hexadecimal, st 0A is LISTEN, and the inode names the socket
			if len(fields) < 10 || fields[3] != "0A" || !inodes[fields[9]] {
				continue
			}
			_, hexPort, ok := strings.Cut(fields[1], ":")
			require.True(t, ok, line)
			port, err := strconv.ParseUint(hexPort, 16, 16)
			require.NoError(t, err)
			ports = append(ports, int(port))
		}
	}
	sort.Ints(ports)
	return slices.Compact(ports)
}

// TestNoEvaluationEndpoint pins UC-017 A8: flagd-sync serves no flag evaluation. The evaluation and OFREP paths find no
// service endpoint on any of its ports, and the process listens on the two ports it was given and on no other: an
// evaluation or OFREP endpoint would open its own.
func TestNoEvaluationEndpoint(t *testing.T) {
	_, paths := writeSyncSources(t)
	ports := map[string]int{"management-port": freePort(t), "sync-port": freePort(t)}
	args := append([]string{"start"}, portArgs(ports)...)
	for _, path := range paths {
		args = append(args, "--uri", "file:"+path)
	}
	p := launch(t, builtBinary(t, "flagd-sync"), args...)
	waitReady(t, p, ports["management-port"])

	for name, port := range ports {
		for _, path := range []string{"/flagd.evaluation.v1.Service/ResolveBoolean", "/flagd.evaluation.v2.Service/ResolveBoolean", "/ofrep/v1/evaluate/flags"} {
			resp, _ := post(t, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), "application/json", []byte(`{"flagKey":"flag-a"}`))
			require.Equal(t, http.StatusNotFound, resp.StatusCode, "%s on %s", path, name)
		}
	}
	want := []int{ports["management-port"], ports["sync-port"]}
	sort.Ints(want)
	require.Equal(t, want, listeningPorts(t, p.cmd.Process.Pid), "flagd-sync listens on its management and flag sync ports only")
}

// TestUnknownSchemeListsSchemesOnFlagdSync pins UC-017 A1: a source whose scheme flagd-sync has no sync provider for
// stops startup with an error that names the source and lists the schemes it handles.
func TestUnknownSchemeListsSchemesOnFlagdSync(t *testing.T) {
	exit, out := runBinary(t, builtBinary(t, "flagd-sync"), "start", "--uri", "nope://flags")
	require.Equal(t, 1, exit, out)
	require.Contains(t, out, "invalid sync uri argument: nope://flags, the distribution flagd-sync has no sync provider for it and handles ")
	for _, scheme := range []string{"file:", "http://", "https://", "grpc://", "gs://", "azblob://", "s3://"} {
		require.Contains(t, out, scheme)
	}
}

// TestAbsentComponentSettingStopsStartup pins UC-017 A2: a setting that belongs to a component flagd-sync lacks stops
// startup and names the setting and the component, and a setting of a component it has does not.
func TestAbsentComponentSettingStopsStartup(t *testing.T) {
	_, paths := writeSyncSources(t)
	bin := builtBinary(t, "flagd-sync")

	exit, out := runBinary(t, bin, "start", "--uri", "file:"+paths[0], "--ofrep-port", "9016", "--port", "9013")
	require.Equal(t, 1, exit, out)
	require.Contains(t, out, "the distribution flagd-sync does not contain the components of these settings: ")
	require.Contains(t, out, "--ofrep-port (")
	require.Contains(t, out, "--port (")
	require.Contains(t, out, `run "flagd components" to list the components of this distribution`)
}

// startOnFile starts the distribution on a source whose content is given and returns whether it became ready and the
// warnings and errors it logged, without clocks and paths. A source that is expected to be accepted is waited for;
// one that is expected to be rejected is given the time to deliver, and the process is stopped after it.
//
// A start that was expected to become ready and did not is made once more on new ports. A configuration the
// distribution rejects is rejected again, so the verdict is the same; a start that stalled for a reason outside the
// configuration is not held against it. The output of the first attempt is logged, so a start that needed the second
// one is visible.
func startOnFile(t *testing.T, name string, portFlags []string, content, ext string, expectReady bool) (ready bool, problems string) {
	t.Helper()
	ready, problems, raw := startOnFileOnce(t, name, portFlags, content, ext, expectReady)
	if expectReady && !ready {
		t.Logf("%s was not ready within the deadline and is started again; its first output:\n%s", name, raw)
		reportStall(t, name, raw)
		ready, problems, _ = startOnFileOnce(t, name, portFlags, content, ext, expectReady)
	}
	return ready, problems
}

// reportStall appends a start that needed its second attempt to the job summary of GitHub Actions, where it shows
// although the passing subtest prints nothing (the tests run without -v).
func reportStall(t *testing.T, name, raw string) {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Logf("the job summary cannot be written: %v", err)
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "### %s: %s was not ready in time and started again\n\n```\n%s\n```\n\n", t.Name(), name, raw)
}

func startOnFileOnce(t *testing.T, name string, portFlags []string, content, ext string, expectReady bool) (ready bool, problems, raw string) {
	t.Helper()
	bin := builtBinary(t, name)
	dir := t.TempDir()
	path := filepath.Join(dir, "flags"+ext)
	writeFile(t, path, content)

	// debug lines are not warnings or errors, so the comparison of what the two log is unaffected
	args := []string{"start", "--debug", "--log-format", "json", "--uri", "file:" + path}
	ports := map[string]int{}
	for _, flagName := range portFlags {
		ports[flagName] = freePort(t)
		args = append(args, "--"+flagName, fmt.Sprint(ports[flagName]))
	}
	p := launch(t, bin, args...)
	if expectReady {
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline) && !ready; time.Sleep(100 * time.Millisecond) {
			ready = isReady(ports["management-port"])
		}
	} else {
		p.settle(t, 30*time.Second)
		ready = isReady(ports["management-port"])
	}
	exited := !p.running()
	if expectReady && !ready && !exited {
		// SIGQUIT makes the Go runtime print the stack of every goroutine and exit, which shows where the start stalled
		_ = p.cmd.Process.Signal(syscall.SIGQUIT)
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
		}
	}
	p.stop(20 * time.Second)
	raw = fmt.Sprintf("the process had exited: %t\n%s", exited, p.out.String())

	var lines []string
	for _, line := range strings.Split(p.out.String(), "\n") {
		if strings.Contains(line, `"level":"warn"`) || strings.Contains(line, `"level":"error"`) || strings.Contains(line, `"level":"fatal"`) {
			line = regexp.MustCompile(`"ts":[^,]+,`).ReplaceAllString(line, "")
			line = regexp.MustCompile(`"caller":"[^"]+",?`).ReplaceAllString(line, "")
			lines = append(lines, orderless(strings.ReplaceAll(line, dir, "<dir>")))
		}
	}
	return ready, strings.Join(lines, "\n"), raw
}

// orderless sorts the lines inside a log entry. The schema validation lists its errors in the order of a map, which
// differs between two runs of the same flagd.
func orderless(entry string) string {
	parts := strings.Split(entry, `\n`)
	sort.Strings(parts)
	return strings.Join(parts, `\n`)
}

// verdict names whether a distribution accepted a flag configuration.
func verdict(ready bool) string {
	if ready {
		return "accepts"
	}
	return "rejects"
}

// flagConfigurations returns the files of the set of flag configurations, valid and invalid, in a stable order.
func flagConfigurations(t *testing.T, kind string) []string {
	t.Helper()
	var files []string
	for _, pattern := range []string{"*.json", "*.yaml", "*.yml"} {
		found, err := filepath.Glob(filepath.Join("testdata", "flag-configurations", kind, pattern))
		require.NoError(t, err)
		files = append(files, found...)
	}
	require.NotEmpty(t, files, "the %s flag configurations", kind)
	sort.Strings(files)
	return files
}

// TestProxyRejectsWhatFlagdFullRejects pins UC-017 A3, BR-002 and BR-003, and UC-013 main step 6, A8 and BR-008, over
// the set of flag configurations in testdata/flag-configurations. flagd-full's verdict is the reference: a file in
// valid/ is one flagd-full accepts (it logs a schema warning for some and keeps going), a file in invalid/ one it
// rejects. flagd-sync gives the same verdict with the same log, and never becomes ready on a rejected one. A mismatch
// names the file with the verdict it is recorded under, flagd-full's and flagd-sync's.
//
// The set is seeded from the flag files of the test-harness and the samples of this repository (valid/), and from the
// invalid sample of the flagd configuration format's contract and the shapes the schema refuses (invalid/). A
// distribution that carries another configuration format adds its samples to the two directories.
func TestProxyRejectsWhatFlagdFullRejects(t *testing.T) {
	for _, kind := range []string{"valid", "invalid"} {
		for _, file := range flagConfigurations(t, kind) {
			t.Run(kind+"/"+filepath.Base(file), func(t *testing.T) {
				content, err := os.ReadFile(file)
				require.NoError(t, err)
				expectReady := kind == "valid"

				fullReady, fullProblems := startOnFile(t, "flagd-full", allPortFlags, string(content), filepath.Ext(file), expectReady)
				syncReady, syncProblems := startOnFile(t, "flagd-sync", syncPortFlags, string(content), filepath.Ext(file), expectReady)

				summary := fmt.Sprintf("%s is recorded under %s/: flagd-full %s it, flagd-sync %s it",
					file, kind, verdict(fullReady), verdict(syncReady))
				require.Equal(t, expectReady, fullReady, "the recorded verdict differs from flagd-full's: %s\n%s", summary, fullProblems)
				require.Equal(t, fullReady, syncReady, "flagd-sync differs from flagd-full: %s\n%s", summary, syncProblems)
				require.Equal(t, fullProblems, syncProblems, "what the two log: %s", summary)
			})
		}
	}
}

// TestRejectedChangeKeepsLastAccepted pins UC-017 A5: a changed flag configuration the format rejects leaves the last
// accepted one in the store, flagd-sync stays ready, and a subscriber gets nothing until a corrected one arrives.
func TestRejectedChangeKeepsLastAccepted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flags.json")
	valid := func(variant string) string {
		return `{"flags":{"f":{"state":"ENABLED","variants":{"on":true,"off":false},"defaultVariant":"` + variant + `"}}}`
	}
	writeFile(t, path, valid("on"))
	ports := startDistribution(t, "flagd-sync", syncPortFlags, nil, path)
	base := flagSyncBase(ports)

	stream := openSyncStream(t, base, `{}`, nil)
	first, ok := stream.next(10 * time.Second)
	require.True(t, ok)
	require.Contains(t, first, `"defaultVariant":"on"`)

	writeFile(t, path, `{"flags":{"f":{"state":"ENABLED","variants":{"on":true},"defaultVariant":"nope"}}}`)
	_, ok = stream.next(5 * time.Second)
	require.False(t, ok, "a rejected flag configuration is sent to no subscriber")
	status, kept := fetchAll(t, base, nil)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, kept, `"defaultVariant":"on"`, "the last accepted flag configuration stays")
	require.True(t, isReady(ports["management-port"]), "flagd-sync stays ready")

	writeFile(t, path, valid("off"))
	corrected, ok := stream.next(30 * time.Second)
	require.True(t, ok, "the corrected flag configuration reaches the subscriber")
	require.Contains(t, corrected, `"defaultVariant":"off"`)
}

// TestUnavailableSourceNotReady pins UC-017 A4: a source flagd-sync cannot read keeps it running and not ready, and it
// becomes ready once the source delivers a flag configuration the format accepts.
func TestUnavailableSourceNotReady(t *testing.T) {
	sourcePort := freePort(t)
	ports := map[string]int{"management-port": freePort(t), "sync-port": freePort(t)}
	args := append([]string{"start", "--uri", fmt.Sprintf("http://127.0.0.1:%d/flags.json", sourcePort), "--log-format", "json"}, portArgs(ports)...)
	p := launch(t, builtBinary(t, "flagd-sync"), args...)

	p.waitLogged(t, fmt.Sprintf("127.0.0.1:%d", sourcePort), 30*time.Second) // the log names the source
	require.True(t, p.running(), "flagd-sync keeps retrying: %s", p.out.String())
	require.False(t, isReady(ports["management-port"]), "not ready while the source is unavailable")

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", sourcePort))
	require.NoError(t, err)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, parityFlags)
	}), ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	waitReady(t, p, ports["management-port"])
}

// TestClientReconnectsAfterRestart pins UC-017 A6: a subscriber whose stream ended because flagd-sync restarted
// subscribes again and gets the flag configuration, with the same ports and sources.
func TestClientReconnectsAfterRestart(t *testing.T) {
	_, paths := writeSyncSources(t)
	bin := builtBinary(t, "flagd-sync")
	ports := map[string]int{"management-port": freePort(t), "sync-port": freePort(t)}
	args := append([]string{"start"}, portArgs(ports)...)
	for _, path := range paths {
		args = append(args, "--uri", "file:"+path)
	}

	first := launch(t, bin, args...)
	waitReady(t, first, ports["management-port"])
	base := flagSyncBase(ports)
	stream := openSyncStream(t, base, `{}`, nil)
	before, ok := stream.next(10 * time.Second)
	require.True(t, ok)

	first.stop(20 * time.Second)
	for ok {
		_, ok = stream.next(10 * time.Second)
	}
	require.False(t, isReady(ports["management-port"]), "flagd-sync is down")

	second := launch(t, bin, args...)
	waitReady(t, second, ports["management-port"])
	var after string
	require.Eventually(t, func() bool {
		req, err := http.NewRequest(http.MethodPost, base+syncService+"FetchAllFlags", strings.NewReader(`{}`))
		if err != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, 200*time.Millisecond)
	again := openSyncStream(t, base, `{}`, nil)
	after, ok = again.next(10 * time.Second)
	require.True(t, ok, "the subscriber gets the flag configuration again")
	require.Equal(t, before, after, "the same flag configuration")
}

// TestAbsentFormatSkipsSource pins UC-017 A9: a source that names a configuration format flagd-sync lacks is skipped
// with an error that names the source and the formats it has, and the other sources deliver.
func TestAbsentFormatSkipsSource(t *testing.T) {
	_, paths := writeSyncSources(t)
	vendor := filepath.Join(t.TempDir(), "vendor.json")
	writeFile(t, vendor, `{"environment":"prod","flags":{"a":1}}`)
	sources, err := json.Marshal([]map[string]any{
		{"uri": paths[0], "provider": "file"},
		{"uri": vendor, "provider": "file", "format": "keyvalue"},
	})
	require.NoError(t, err)

	ports := map[string]int{"management-port": freePort(t), "sync-port": freePort(t)}
	args := append([]string{"start", "--log-format", "json", "--sources", string(sources)}, portArgs(ports)...)
	p := launch(t, builtBinary(t, "flagd-sync"), args...)

	waitReady(t, p, ports["management-port"])
	logs := p.out.String()
	require.Contains(t, logs, vendor, "the error names the source")
	require.Contains(t, logs, "keyvalue")
	require.Contains(t, logs, "flagd", "and the formats flagd-sync has")
	status, body := fetchAll(t, flagSyncBase(ports), nil)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "flag-a", "the other source delivers")
}

// vendorSources writes the two sources of the vendor configuration proxy: a flagd flag file and a keyvalue file.
func vendorSources(t *testing.T) (dir, flagdFile, vendorFile string) {
	t.Helper()
	dir = t.TempDir()
	flagdFile, vendorFile = filepath.Join(dir, "flagd.json"), filepath.Join(dir, "vendor.json")
	writeFile(t, flagdFile, `{"flags":{"flag-a":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`)
	writeFile(t, vendorFile, `{ "environment": "prod",  "flags": {"color":"blue", "limit":10} }`)
	return dir, flagdFile, vendorFile
}

// startSyncVendor starts the sync-vendor test distribution on the two sources and returns its ports.
func startSyncVendor(t *testing.T, flagdFile, vendorFile string) (ports map[string]int, p *process) {
	t.Helper()
	path := filepath.Join(root(t), "test", "distributions", "sync-vendor", "manifest.yaml")
	b := build(t, path)
	require.NoError(t, b.err, b.out)
	ports = map[string]int{"management-port": freePort(t), "sync-port": freePort(t), "port": freePort(t)}
	args := append([]string{"start", "--uri", flagdFile, "--vendor-uri", vendorFile}, portArgs(ports)...)
	p = launch(t, binary(t, path), args...)
	waitReady(t, p, ports["management-port"])
	return ports, p
}

// TestVendorConfigurationProxy pins UC-017 A7 and BR-004 on the sync-vendor test distribution: flag sync sends the
// documents of the configuration format a request names unchanged and flagd's to a request that names none, and the
// vendor's own service endpoint answers with the validator of the documents, with no change while their version stays.
func TestVendorConfigurationProxy(t *testing.T) {
	_, flagdFile, vendorFile := vendorSources(t)
	ports, _ := startSyncVendor(t, flagdFile, vendorFile)
	base := flagSyncBase(ports)

	status, vendor := fetchAll(t, base, map[string]string{"Flagd-Configuration-Format": "keyvalue"})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, `{ "environment": "prod",  "flags": {"color":"blue", "limit":10} }`, vendor, "the document, unchanged")

	status, flagdDoc := fetchAll(t, base, nil)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, flagdDoc, "flag-a", "without the header, flagd's documents")
	require.NotContains(t, flagdDoc, "color")

	status, message := fetchAll(t, base, map[string]string{"Flagd-Configuration-Format": "nope"})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, message, "flagd", "the formats the distribution has")
	require.Contains(t, message, "keyvalue")

	own := fmt.Sprintf("http://127.0.0.1:%d/v0/changes", ports["port"])
	get := func(known string) (int, string) {
		req, err := http.NewRequest(http.MethodGet, own+"?wait=500ms", nil)
		require.NoError(t, err)
		req.Header.Set("Flagd-Selector", "source="+vendorFile)
		if known != "" {
			req.Header.Set("If-None-Match", `"`+known+`"`)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode, strings.Trim(resp.Header.Get("ETag"), `"`)
	}
	status, validator := get("")
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, validator)
	status, _ = get(validator)
	require.Equal(t, http.StatusNotModified, status, "a repeated request finds no change")

	writeFile(t, vendorFile, `{"environment":"prod","flags":{"color":"green","limit":10}}`)
	require.Eventually(t, func() bool {
		status, changed := get(validator)
		return status == http.StatusOK && changed != validator
	}, 30*time.Second, 200*time.Millisecond, "a change of the document gives a new validator")
	_, text := fetchAll(t, base, map[string]string{"Flagd-Configuration-Format": "keyvalue"})
	require.Contains(t, text, "green", "and flag sync sends the changed document")
}
