package upgrade

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// baselineCommit is the upstream merge-base of this repository: the last commit of upstream flagd that the branch
// carries (UC-009 Q1). Behaviour is compared against flagd built from it with upstream's own Dockerfile. When upstream
// is merged in, this moves with the merge.
const baselineCommit = "b23f82ec"

// The same build arguments for both images, so that flagd version prints the same line.
var buildArgs = []string{"VERSION=v0.0.0-upgrade", "COMMIT=0000000", "DATE=2026-01-01T00:00:00Z"}

// The ports flagd listens on by default.
const (
	portEvaluation = "8013"
	portManagement = "8014"
	portSync       = "8015"
	portOFREP      = "8016"
)

var (
	buildOnce sync.Once
	buildErr  error
)

// repoRoot is the root of this repository.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return dir
}

func docker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// images builds the baseline and the candidate once per run and returns their tags.
func images(t *testing.T) (baseline, candidate string) {
	t.Helper()
	if os.Getenv("UPGRADE_TEST") == "" {
		t.Skip("the upgrade differential needs Docker; make upgrade-test sets UPGRADE_TEST")
	}
	buildOnce.Do(func() {
		root := repoRoot(t)
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
		defer cancel()

		// the baseline's context is the merge-base, exported from this repository's history: nothing is fetched
		// UPGRADE_BASELINE_DIR names a tree that is exported already, for a checkout git cannot read from where the test
		// runs, such as a Windows worktree seen from WSL
		context := os.Getenv("UPGRADE_BASELINE_DIR")
		if context == "" {
			var err error
			if context, err = os.MkdirTemp("", "upgrade-baseline-"); err != nil {
				buildErr = err
				return
			}
			defer os.RemoveAll(context)
			archive := exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("git -C %q archive %s | tar -x -C %q", root, baselineCommit, context))
			if out, err := archive.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("exporting %s: %w: %s", baselineCommit, err, out)
				return
			}
		}
		for tag, dir := range map[string]string{"flagd-upgrade-baseline": context, "flagd-upgrade-candidate": root} {
			args := []string{"build", "-q", "-f", filepath.Join(dir, "flagd", "build.Dockerfile"), "-t", tag}
			for _, a := range buildArgs {
				args = append(args, "--build-arg", a)
			}
			if out, err := docker(ctx, append(args, dir)...); err != nil {
				buildErr = fmt.Errorf("building %s: %w: %s", tag, err, out)
				return
			}
		}
	})
	require.NoError(t, buildErr)
	return "flagd-upgrade-baseline", "flagd-upgrade-candidate"
}

// run is a flagd container.
type run struct {
	t     *testing.T
	id    string
	dir   string            // the host directory mounted at /data
	ports map[string]string // container port -> 127.0.0.1:<mapped>
}

// start runs the image with the arguments, the environment and the files (name -> content, mounted read-only under
// /data), and publishes flagd's four ports on ephemeral host ports.
func start(t *testing.T, image string, args []string, env map[string]string, files map[string]string) *run {
	t.Helper()
	return startWith(t, image, args, env, files, nil)
}

// startWith is start with more arguments for docker run.
func startWith(t *testing.T, image string, args []string, env map[string]string, files map[string]string, dockerFlags []string) *run {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	require.NoError(t, os.Chmod(dir, 0o755))
	dockerArgs := []string{"run", "-d", "-v", dir + ":/data:ro"}
	dockerArgs = append(dockerArgs, dockerFlags...)
	for _, p := range []string{portEvaluation, portManagement, portSync, portOFREP} {
		dockerArgs = append(dockerArgs, "-p", "127.0.0.1::"+p)
	}
	for k, v := range env {
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	dockerArgs = append(append(dockerArgs, image), args...)
	id, err := docker(context.Background(), dockerArgs...)
	require.NoError(t, err, id)
	r := &run{t: t, id: id, dir: dir, ports: map[string]string{}}
	t.Cleanup(func() { _, _ = docker(context.Background(), "rm", "-f", id) })
	return r
}

// mapPorts reads the host ports once the container is running.
func (r *run) mapPorts() {
	r.t.Helper()
	for _, p := range []string{portEvaluation, portManagement, portSync, portOFREP} {
		out, err := docker(context.Background(), "port", r.id, p+"/tcp")
		require.NoError(r.t, err, out)
		r.ports[p] = strings.Split(out, "\n")[0]
	}
}

// ready waits for /readyz to answer 200, and reports whether it did within the time.
func (r *run) ready(within time.Duration) bool {
	r.t.Helper()
	r.mapPorts()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if status, _, err := r.get(portManagement, "/readyz", nil); err == nil && status == http.StatusOK {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func (r *run) get(port, path string, header map[string]string) (int, http.Header, error) {
	status, h, _, err := r.do(http.MethodGet, port, path, "", header)
	return status, h, err
}

// do sends one request to a published port and returns the status, the headers and the body.
func (r *run) do(method, port, path, body string, header map[string]string) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, "http://"+r.ports[port]+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, data, err
}

// stream sends one request and reads what the server sends back for the time given, as the status, the headers and the
// body read so far. A Connect streaming call sends the body as one frame and gets frames back; see frames.
func (r *run) stream(method, port, path string, body []byte, header map[string]string, within time.Duration) (int, http.Header, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://"+r.ports[port]+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body) // ends at the deadline, with what arrived until then
	return resp.StatusCode, resp.Header, data, nil
}

// frame wraps a JSON message in a Connect streaming envelope.
func frame(message string) []byte {
	out := make([]byte, 5, 5+len(message))
	binary.BigEndian.PutUint32(out[1:], uint32(len(message)))
	return append(out, message...)
}

// frames renders the messages of a Connect streaming response, one normalized line each.
func frames(data []byte) []string {
	var out []string
	for len(data) >= 5 {
		n := int(binary.BigEndian.Uint32(data[1:5]))
		if len(data) < 5+n {
			break
		}
		out = append(out, fmt.Sprintf("flags=%d %s", data[0], JSON(data[5:5+n])))
		data = data[5+n:]
	}
	return out
}

// logs returns what the container wrote to stdout and stderr.
func (r *run) logs() string {
	out, _ := docker(context.Background(), "logs", r.id)
	return out
}

// exited waits for the container to exit and returns its exit code.
func (r *run) exited(within time.Duration) (int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	out, err := docker(ctx, "wait", r.id)
	if err != nil {
		return 0, false
	}
	var code int
	_, _ = fmt.Sscanf(out, "%d", &code)
	return code, true
}

// stop sends SIGTERM and waits for the exit.
func (r *run) stop(within time.Duration) (int, bool) {
	_, _ = docker(context.Background(), "kill", "--signal", "SIGTERM", r.id)
	return r.exited(within)
}

// once runs the image to completion with these arguments and returns the exit code and the output (stdout and
// stderr together, as a terminal shows them).
func once(t *testing.T, image string, args []string, env map[string]string, files map[string]string) (int, string) {
	t.Helper()
	r := start(t, image, args, env, files)
	code, ok := r.exited(60 * time.Second)
	require.True(t, ok, "%s %v did not exit", image, args)
	return code, r.logs()
}

// Observation is one thing a scenario saw, under a name that is the same for both flagds.
type Observation struct{ Key, Value string }

// Transcript is what one flagd showed in a scenario.
type Transcript []Observation

func (tr *Transcript) add(key, value string) { *tr = append(*tr, Observation{key, value}) }

// Difference is one observation that the two flagds disagree on.
type Difference struct {
	Scenario, Key, Baseline, Candidate string
}

// diff compares two transcripts observation by observation.
func diff(scenario string, baseline, candidate Transcript) []Difference {
	b, c := map[string]string{}, map[string]string{}
	var keys []string
	for _, o := range baseline {
		if _, seen := b[o.Key]; !seen {
			keys = append(keys, o.Key)
		}
		b[o.Key] = o.Value
	}
	for _, o := range candidate {
		if _, seen := b[o.Key]; !seen {
			if _, seenC := c[o.Key]; !seenC {
				keys = append(keys, o.Key)
			}
		}
		c[o.Key] = o.Value
	}
	var out []Difference
	for _, k := range keys {
		bv, bok := b[k]
		cv, cok := c[k]
		switch {
		case !bok:
			bv = "<not observed>"
		case !cok:
			cv = "<not observed>"
		}
		if bv != cv {
			out = append(out, Difference{scenario, k, bv, cv})
		}
	}
	return out
}
