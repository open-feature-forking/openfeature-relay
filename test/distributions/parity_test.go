package distributions

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// parityFlags are the flags the standard distributions and flagd-full are compared on: one of each type, a flag with
// targeting, a disabled flag, and a flag that does not exist is asked for as well.
const parityFlags = `{"flags":{
  "bool-flag":{"state":"ENABLED","variants":{"on":true,"off":false},"defaultVariant":"on"},
  "string-flag":{"state":"ENABLED","variants":{"a":"alpha","b":"beta"},"defaultVariant":"a",
    "targeting":{"if":[{"==":[{"var":"tier"},"gold"]},"b","a"]}},
  "int-flag":{"state":"ENABLED","variants":{"one":1,"two":2},"defaultVariant":"two"},
  "float-flag":{"state":"ENABLED","variants":{"pi":3.14,"e":2.72},"defaultVariant":"pi"},
  "object-flag":{"state":"ENABLED","variants":{"x":{"k":"v"},"y":{"k":"w"}},"defaultVariant":"x"},
  "disabled-flag":{"state":"DISABLED","variants":{"on":true},"defaultVariant":"on"}}}`

// validators are the opaque values that differ between two runs of the same flagd.
var validators = regexp.MustCompile(`"?(flagConfigEtag|etag)"?\s*:\s*"[0-9a-f]{16,}"`)

// evaluationTranscript sends the same single and bulk evaluation requests over ConnectRPC and OFREP and renders the
// status and the body of each answer, field by field.
func evaluationTranscript(t *testing.T, ports map[string]int) string {
	t.Helper()
	var out strings.Builder
	do := func(title, port, path, body string) {
		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d%s", ports[port], path), strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, title)
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		rendered := string(data)
		if strings.HasPrefix(strings.TrimSpace(rendered), "{") {
			rendered = validators.ReplaceAllString(canonical(t, data), `"$1": "<validator>"`)
		}
		fmt.Fprintf(&out, "== %s\n%d\n%s\n", title, resp.StatusCode, rendered)
	}

	const gold = `"context":{"tier":"gold"}`
	types := map[string]string{"bool-flag": "Boolean", "string-flag": "String", "int-flag": "Int", "float-flag": "Float", "object-flag": "Object"}
	for _, flag := range []string{"bool-flag", "string-flag", "int-flag", "float-flag", "object-flag", "disabled-flag", "missing"} {
		kind := types[flag]
		if kind == "" {
			kind = "Boolean"
		}
		do("connect "+flag, "port", "/flagd.evaluation.v1.Service/Resolve"+kind, fmt.Sprintf(`{"flagKey":%q,%s}`, flag, gold))
		do("ofrep "+flag, "ofrep-port", "/ofrep/v1/evaluate/flags/"+flag, `{`+gold+`}`)
	}
	do("connect bulk", "port", "/flagd.evaluation.v1.Service/ResolveAll", `{`+gold+`}`)
	do("ofrep bulk", "ofrep-port", "/ofrep/v1/evaluate/flags", `{`+gold+`}`)
	return out.String()
}

func writeParityFlags(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, os.WriteFile(path, []byte(parityFlags), 0o600))
	return path
}

// TestStandardDistributionParity pins UC-010 step 9 and BR-006, and UC-017 BR-002 and BR-005: the evaluating standard
// distributions answer the evaluation requests as flagd-full does, field by field, and flagd-sync sends the flag
// configuration byte for byte as flagd-full does.
func TestStandardDistributionParity(t *testing.T) {
	flags := writeParityFlags(t)
	want := evaluationTranscript(t, startDistribution(t, "flagd-full", allPortFlags, nil, flags))
	for _, d := range standard {
		if !d.evaluates {
			continue
		}
		t.Run(d.name, func(t *testing.T) {
			got := evaluationTranscript(t, startDistribution(t, d.name, d.portFlags, nil, flags))
			require.Equal(t, want, got)
		})
	}

	t.Run("flagd-sync sends flagd-full's flag configuration", func(t *testing.T) {
		dir, paths := writeSyncSources(t)
		ports := startDistribution(t, "flagd-sync", []string{"management-port", "sync-port"}, nil, paths...)
		got := flagSyncTranscript(t, fmt.Sprintf("http://127.0.0.1:%d", ports["sync-port"]), dir)
		require.Equal(t, recordedSyncPayload(t), got)
	})
}

// invalidFlags is a flag configuration the schema rejects.
const invalidFlags = `{"metadata":{"flagSetId":"prod"},"flags":{"a":{"state":"ENABLED","defaultVariant":"nope","variants":{"on":true}}}}`

// startOnInvalid starts the distribution on a source whose flag configuration is invalid and returns whether it
// became ready and the warnings and errors it logged, without clocks and paths.
func startOnInvalid(t *testing.T, name string, portFlags []string) (ready bool, problems string) {
	t.Helper()
	bin := builtBinary(t, name)
	dir := t.TempDir()
	path := filepath.Join(dir, "flags.json")
	require.NoError(t, os.WriteFile(path, []byte(invalidFlags), 0o600))

	args := []string{"start", "--log-format", "json", "--uri", "file:" + path}
	ports := map[string]int{}
	for _, flagName := range portFlags {
		ports[flagName] = freePort(t)
		args = append(args, "--"+flagName, fmt.Sprint(ports[flagName]))
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	require.NoError(t, cmd.Start())
	time.Sleep(6 * time.Second)
	if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", ports["management-port"])); err == nil { //nolint:noctx // a local probe
		ready = resp.StatusCode == http.StatusOK
		resp.Body.Close()
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	var lines []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, `"level":"warn"`) || strings.Contains(line, `"level":"error"`) {
			line = regexp.MustCompile(`"ts":[^,]+,`).ReplaceAllString(line, "")
			line = regexp.MustCompile(`"caller":"[^"]+",?`).ReplaceAllString(line, "")
			lines = append(lines, strings.ReplaceAll(line, dir, "<dir>"))
		}
	}
	return ready, strings.Join(lines, "\n")
}

// TestInvalidFlagConfigurationRejectedAlike pins UC-017 BR-002: a flag configuration the schema rejects is rejected by
// flagd-sync as flagd-full rejects it: the source never delivers, so neither becomes ready, and both log the same.
func TestInvalidFlagConfigurationRejectedAlike(t *testing.T) {
	fullReady, fullProblems := startOnInvalid(t, "flagd-full", allPortFlags)
	require.False(t, fullReady, "flagd-full rejects the configuration")
	require.NotEmpty(t, fullProblems)

	ready, problems := startOnInvalid(t, "flagd-sync", []string{"management-port", "sync-port"})
	require.False(t, ready, "flagd-sync rejects the configuration")
	require.Equal(t, fullProblems, problems)
}

// TestUnknownSchemeListsSchemes pins UC-010 A2: a source whose scheme the distribution has no sync provider for stops
// startup with an error that names the source and lists the schemes its sync providers handle.
func TestUnknownSchemeListsSchemes(t *testing.T) {
	exit, out := runBinary(t, builtBinary(t, "flagd-minimal"), "start", "--uri", "s3://bucket/flags.json")
	require.Equal(t, 1, exit, out)
	require.Contains(t, out, "invalid sync uri argument: s3://bucket/flags.json, the distribution flagd-minimal has no sync provider for it and handles ")
	for _, scheme := range []string{"file:", "http://", "https://", "grpc://"} {
		require.Contains(t, out, scheme)
	}
	for _, scheme := range []string{"gs://", "azblob://"} {
		require.NotContains(t, out, scheme, "flagd-minimal has no sync provider for it")
	}
	require.Contains(t, out, `run "flagd components" to list the components of this distribution`)
}
