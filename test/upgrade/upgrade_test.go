package upgrade

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The flag configuration both flagds are started with, and the context the requests carry.
const flagsJSON = `{"flags":{
  "bool-flag":{"state":"ENABLED","variants":{"on":true,"off":false},"defaultVariant":"on"},
  "string-flag":{"state":"ENABLED","variants":{"a":"alpha","b":"beta"},"defaultVariant":"a",
    "targeting":{"if":[{"==":[{"var":"tier"},"gold"]},"b","a"]}},
  "int-flag":{"state":"ENABLED","variants":{"one":1,"two":2},"defaultVariant":"two"},
  "float-flag":{"state":"ENABLED","variants":{"pi":3.14,"e":2.72},"defaultVariant":"pi"},
  "object-flag":{"state":"ENABLED","variants":{"x":{"k":"v"},"y":{"k":"w"}},"defaultVariant":"x"},
  "disabled-flag":{"state":"DISABLED","variants":{"on":true},"defaultVariant":"on"}}}`

const requestContext = `"context":{"tier":"gold"}`

// startFile starts the image on the flag file and waits for readiness.
func startFile(t *testing.T, image string, extraArgs ...string) *run {
	t.Helper()
	args := append([]string{"start", "--uri", "file:/data/flags.json"}, extraArgs...)
	r := start(t, image, args, nil, map[string]string{"flags.json": flagsJSON})
	require.True(t, r.ready(60*time.Second), "%s did not become ready: %s", image, r.logs())
	return r
}

// A scenario runs against one image and returns what it saw.
type scenario func(t *testing.T, image string) Transcript

// The scenarios of the differential. Each is named for the flow of UC-009 it proves.
var scenarios = map[string]scenario{
	"version":              scenarioVersion,
	"unknown-scheme":       scenarioUnknownScheme,
	"deprecated-keepalive": scenarioDeprecatedKeepAlive,
	"resolve":              scenarioResolve,
	"management":           scenarioManagement,
	"unreachable-source":   scenarioUnreachableSource,
	"no-staleness-default": scenarioNoStalenessDefault,
	"sigterm":              scenarioSigterm,
}

func scenarioVersion(t *testing.T, image string) Transcript {
	var tr Transcript
	for _, args := range [][]string{{"version"}, {"--help"}} {
		code, out := once(t, image, args, nil, nil)
		tr.add(strings.Join(args, " ")+"/exit-code", fmt.Sprint(code))
		tr.add(strings.Join(args, " ")+"/output", Text(out))
	}
	return tr
}

// A2: an unknown URI scheme ends flagd with the same exit code and the same first error line.
func scenarioUnknownScheme(t *testing.T, image string) Transcript {
	code, out := once(t, image, []string{"start", "--log-format", "json", "--uri", "nope://x"}, nil, nil)
	var tr Transcript
	tr.add("exit-code", fmt.Sprint(code))
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "nope://x") {
			tr.add("first-error-line", Text(stripJSONFields(line)))
			break
		}
	}
	return tr
}

// A3: the deprecated keepalive flags are accepted, with the same warning.
func scenarioDeprecatedKeepAlive(t *testing.T, image string) Transcript {
	r := startFile(t, image, "--log-format", "json", "--keep-alive-min-time", "10s", "--keep-alive-permit-without-stream")
	var tr Transcript
	for _, line := range strings.Split(r.logs(), "\n") {
		if strings.Contains(strings.ToLower(line), "keep-alive") || strings.Contains(strings.ToLower(line), "keepalive") {
			tr.add("warning", Text(stripJSONFields(line)))
		}
	}
	tr.add("ready", "true")
	return tr
}

// Main flow 1 to 7: every endpoint answers the same requests the same way.
func scenarioResolve(t *testing.T, image string) Transcript {
	r := startFile(t, image)
	var tr Transcript
	post := func(key, port, path, body string, header map[string]string) (http.Header, []byte) {
		status, h, b, err := r.do(http.MethodPost, port, path, body, header)
		require.NoError(t, err, key)
		tr.add(key+"/status", fmt.Sprint(status))
		tr.add(key+"/body", JSON(b))
		return h, b
	}

	types := map[string]string{"bool-flag": "Boolean", "string-flag": "String", "int-flag": "Int", "float-flag": "Float", "object-flag": "Object"}
	for _, flag := range []string{"bool-flag", "string-flag", "int-flag", "float-flag", "object-flag"} {
		body := fmt.Sprintf(`{"flagKey":%q,%s}`, flag, requestContext)
		post("connect-v1/"+flag, portEvaluation, "/flagd.evaluation.v1.Service/Resolve"+types[flag], body, nil)
		post("connect-v2/"+flag, portEvaluation, "/flagd.evaluation.v2.Service/Resolve"+types[flag], body, nil)
		post("connect-legacy/"+flag, portEvaluation, "/schema.v1.Service/Resolve"+types[flag], body, nil)
		post("ofrep/"+flag, portOFREP, "/ofrep/v1/evaluate/flags/"+flag, `{`+requestContext+`}`, nil)
	}
	post("connect-v1/missing", portEvaluation, "/flagd.evaluation.v1.Service/ResolveBoolean", `{"flagKey":"missing","context":{}}`, nil)
	post("connect-v1/disabled", portEvaluation, "/flagd.evaluation.v1.Service/ResolveBoolean", `{"flagKey":"disabled-flag","context":{}}`, nil)
	post("ofrep/missing", portOFREP, "/ofrep/v1/evaluate/flags/missing", `{"context":{}}`, nil)
	post("connect-v1/bulk", portEvaluation, "/flagd.evaluation.v1.Service/ResolveAll", `{`+requestContext+`}`, nil)

	h, _ := post("ofrep/bulk", portOFREP, "/ofrep/v1/evaluate/flags", `{`+requestContext+`}`, nil)
	if tag := h.Get("ETag"); tag != "" {
		status, _, _, err := r.do(http.MethodPost, portOFREP, "/ofrep/v1/evaluate/flags", `{`+requestContext+`}`, map[string]string{"If-None-Match": tag})
		require.NoError(t, err)
		tr.add("ofrep/bulk-conditional/status", fmt.Sprint(status))
	}
	tr.add("ofrep/etag", fmt.Sprint(h.Get("ETag") != ""))

	status, sh, body, err := r.do(http.MethodPost, portSync, "/flagd.sync.v1.FlagSyncService/FetchAllFlags", `{}`, nil)
	require.NoError(t, err)
	tr.add("flag-sync/status", fmt.Sprint(status))
	tr.add("flag-sync/body", JSON(body))
	tr.add("flag-sync/last-modified", fmt.Sprint(sh.Get("Last-Modified") != ""))
	return tr
}

// The management port: probes and the names of the metrics.
func scenarioManagement(t *testing.T, image string) Transcript {
	r := startFile(t, image)
	// give the metrics something to count
	for i := 0; i < 3; i++ {
		_, _, _, err := r.do(http.MethodPost, portEvaluation, "/flagd.evaluation.v1.Service/ResolveBoolean", `{"flagKey":"bool-flag","context":{}}`, nil)
		require.NoError(t, err)
	}
	var tr Transcript
	for _, path := range []string{"/livez", "/readyz", "/healthz", "/nope"} {
		status, _, err := r.get(portManagement, path, nil)
		require.NoError(t, err)
		tr.add("probe"+path, fmt.Sprint(status))
	}
	status, _, body, err := r.do(http.MethodGet, portManagement, "/metrics", "", nil)
	require.NoError(t, err)
	tr.add("metrics/status", fmt.Sprint(status))
	tr.add("metrics/names", strings.Join(MetricNames(string(body)), "\n"))
	return tr
}

// A4: a source that cannot be reached. The behaviour is the accepted #78 item: a failed first fetch no longer exits.
func scenarioUnreachableSource(t *testing.T, image string) Transcript {
	r := start(t, image, []string{"start", "--uri", "http://127.0.0.1:1/flags.json"}, nil, nil)
	var tr Transcript
	if code, exited := r.exited(15 * time.Second); exited {
		tr.add("exited", "true")
		tr.add("exit-code", fmt.Sprint(code))
		return tr
	}
	tr.add("exited", "false")
	tr.add("ready", fmt.Sprint(r.ready(2*time.Second)))
	return tr
}

// BR-008: with no staleAfter setting, readiness does not lapse however long the source stays quiet.
func scenarioNoStalenessDefault(t *testing.T, image string) Transcript {
	r := startFile(t, image)
	time.Sleep(5 * time.Second)
	status, _, err := r.get(portManagement, "/readyz", nil)
	require.NoError(t, err)
	var tr Transcript
	tr.add("readyz-after-quiet", fmt.Sprint(status))
	return tr
}

// SIGTERM: flagd exits with the same code within a bounded time. The streams' shutdown differences are #78's.
func scenarioSigterm(t *testing.T, image string) Transcript {
	r := startFile(t, image)
	began := time.Now()
	code, exited := r.stop(30 * time.Second)
	var tr Transcript
	tr.add("exited", fmt.Sprint(exited))
	tr.add("exit-code", fmt.Sprint(code))
	tr.add("within-10s", fmt.Sprint(time.Since(began) < 10*time.Second))
	return tr
}

// stripJSONFields removes the fields of a JSON log line that differ between two runs (the time and the caller).
func stripJSONFields(line string) string {
	for _, field := range []string{`"ts":`, `"caller":`} {
		if i := strings.Index(line, field); i >= 0 {
			rest := line[i+len(field):]
			end := strings.IndexAny(rest, ",}")
			if end < 0 {
				end = len(rest)
			}
			line = line[:i] + line[i+len(field)+end:]
		}
	}
	return line
}

// TestUpgrade runs every scenario against today's flagd and the flagd-full the Builder made, and fails on a difference
// that accepted.yaml does not list.
func TestUpgrade(t *testing.T) {
	baseline, candidate := images(t)
	accepted, err := LoadAccepted("accepted.yaml")
	require.NoError(t, err)

	for name, run := range scenarios {
		t.Run(name, func(t *testing.T) {
			before := run(t, baseline)
			after := run(t, candidate)
			require.NotEmpty(t, before, "the baseline observed nothing")
			for _, d := range diff(name, before, after) {
				if entry, ok := accepted.Allows(d.Scenario, d.Key); ok {
					t.Logf("accepted (%s %q): %s/%s\n  baseline:  %s\n  candidate: %s", entry.Kind, entry.Item, d.Scenario, d.Key, d.Baseline, d.Candidate)
					continue
				}
				t.Errorf("%s/%s differs\n  baseline:  %s\n  candidate: %s", d.Scenario, d.Key, d.Baseline, d.Candidate)
			}
		})
	}
}

// TestRollback is A5: after the Operator ran the candidate, the baseline starts with the same configuration and serves
// as it did before.
func TestRollback(t *testing.T) {
	baseline, candidate := images(t)
	files := map[string]string{"flags.json": flagsJSON}
	args := []string{"start", "--uri", "file:/data/flags.json"}

	first := start(t, candidate, args, nil, files)
	require.True(t, first.ready(60*time.Second), first.logs())
	_, _ = first.stop(30 * time.Second)

	again := start(t, baseline, args, nil, files)
	require.True(t, again.ready(60*time.Second), again.logs())
	status, _, body, err := again.do(http.MethodPost, portEvaluation, "/flagd.evaluation.v1.Service/ResolveBoolean", `{"flagKey":"bool-flag","context":{}}`, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, string(body), `"value":true`)
}
