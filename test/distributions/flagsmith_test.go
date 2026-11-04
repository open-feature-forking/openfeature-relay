package distributions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine"
	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine/engine_eval"
	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine/environments"
	"github.com/stretchr/testify/require"
)

const flagsmithKey = "ser.test-key"

// startFlagsmith starts the built flagsmith distribution with one source that reads the environment document the stub
// serves, and returns the address of its OFREP endpoint once it is ready.
func startFlagsmith(t *testing.T, apiBase string) string {
	t.Helper()
	path := filepath.Join(root(t), "distributions", "flagsmith", "manifest.yaml")
	b := build(t, path)
	require.NoError(t, b.err, b.out)

	ports := map[string]string{}
	for _, name := range []string{"port", "management-port", "sync-port", "ofrep-port"} {
		ports[name] = fmt.Sprint(freePort(t))
	}
	source, err := json.Marshal([]map[string]any{{
		"uri": "flagsmith+" + apiBase, "provider": "flagsmith", "format": "flagsmith",
		"headersFrom": map[string]any{"X-Environment-Key": map[string]string{"env": "FLAGSMITH_TEST_KEY"}},
	}})
	require.NoError(t, err)

	cmd := exec.Command(binary(t, path), "start", "--sources", string(source),
		"--port", ports["port"], "--management-port", ports["management-port"],
		"--sync-port", ports["sync-port"], "--ofrep-port", ports["ofrep-port"])
	cmd.Env = append(os.Environ(), "FLAGSMITH_TEST_KEY="+flagsmithKey)
	var output lockedBuffer
	stderr := &output
	cmd.Stdout, cmd.Stderr = &output, &output
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		// whatever the distribution wrote while it served and while it shut down
		require.NotContains(t, output.String(), flagsmithKey, "the key is in no output of the distribution")
	})

	ready := fmt.Sprintf("http://127.0.0.1:%s/readyz", ports["management-port"])
	require.Eventually(t, func() bool {
		resp, err := http.Get(ready) //nolint:noctx // a local probe
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 60*time.Second, 200*time.Millisecond, "the distribution becomes ready: %s", stderr.String())
	return "http://127.0.0.1:" + ports["ofrep-port"]
}

// lockedBuffer collects the output of the distribution, which the test reads while the process still writes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// openFeatureReason is the reason UC-019 Q4 maps an engine reason to.
func openFeatureReason(engineReason string) string {
	switch {
	case engineReason == "DEFAULT":
		return "STATIC"
	case strings.HasPrefix(engineReason, "TARGETING_MATCH"):
		return "TARGETING_MATCH"
	case strings.HasPrefix(engineReason, "SPLIT"):
		return "SPLIT"
	}
	return "UNKNOWN"
}

type ofrepResult struct {
	Key       string         `json:"key"`
	Value     any            `json:"value"`
	Variant   string         `json:"variant"`
	Reason    string         `json:"reason"`
	ErrorCode string         `json:"errorCode"`
	Metadata  map[string]any `json:"metadata"`
}

func evaluateOFREP(t *testing.T, ofrep, flag string, context map[string]any) ofrepResult {
	t.Helper()
	body, err := json.Marshal(map[string]any{"context": context})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, ofrep+"/ofrep/v1/evaluate/flags/"+flag, bytes.NewReader(body)) //nolint:noctx // a local request
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Flagd-Selector", "flagSetId=ENVKEY1")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var result ofrepResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Equal(t, http.StatusOK, resp.StatusCode, "%s: %+v", flag, result)
	return result
}

// TestFlagsmithServesAnEnvironmentDocument is the end-to-end check of UC-019 in its smallest form: the built flagsmith
// distribution fetches one recorded environment document from a stub of the Flagsmith API with the key from its
// environment, and resolves flags through OFREP, for an environment and for an identity, as Flagsmith's engine does.
func TestFlagsmithServesAnEnvironmentDocument(t *testing.T) {
	document, err := os.ReadFile(filepath.Join("testdata", "flagsmith-environment.json"))
	require.NoError(t, err)
	var env environments.EnvironmentModel
	require.NoError(t, json.Unmarshal(document, &env))
	ec := engine_eval.MapEnvironmentDocumentToEvaluationContext(&env)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/environment-document/" || r.Header.Get("X-Environment-Key") != flagsmithKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(document)
	}))
	t.Cleanup(api.Close)
	ofrep := startFlagsmith(t, api.URL+"/api/v1/")

	t.Run("environment", func(t *testing.T) {
		want := flagengine.GetEvaluationResult(&ec).Flags
		for _, flag := range []string{"welcome", "banner", "experiment"} {
			got := evaluateOFREP(t, ofrep, flag, nil)
			require.Equal(t, want[flag].Value, got.Value, flag)
			require.Equal(t, openFeatureReason(want[flag].Reason), got.Reason, flag)
			require.Empty(t, got.Variant, "an environment has no identity to split: %s", flag)
		}
	})

	t.Run("identity", func(t *testing.T) {
		variants := map[string]bool{}
		reasons := map[string]bool{}
		for i := range 40 {
			identifier := fmt.Sprintf("user-%d", i)
			traits := map[string]any{"plan": "beta"}
			if i%2 == 1 {
				traits["plan"] = "free"
			}
			withIdentity := ec
			withIdentity.Identity = &engine_eval.IdentityContext{
				Identifier: identifier, Key: ec.Environment.Key + "_" + identifier, Traits: traits,
			}
			want := flagengine.GetEvaluationResult(&withIdentity).Flags

			context := map[string]any{"targetingKey": identifier}
			for k, v := range traits {
				context[k] = v
			}
			banner := evaluateOFREP(t, ofrep, "banner", context)
			require.Equal(t, want["banner"].Value, banner.Value, identifier)
			require.Equal(t, openFeatureReason(want["banner"].Reason), banner.Reason, identifier)
			require.Equal(t, want["banner"].Variant, banner.Variant, identifier)
			if traits["plan"] == "beta" {
				require.Equal(t, "TARGETING_MATCH", banner.Reason, identifier)
				require.Equal(t, "beta-banner", banner.Value)
			} else {
				require.Equal(t, "STATIC", banner.Reason, identifier)
			}

			experiment := evaluateOFREP(t, ofrep, "experiment", context)
			require.Equal(t, want["experiment"].Value, experiment.Value, identifier)
			require.Equal(t, want["experiment"].Variant, experiment.Variant, identifier)
			require.Equal(t, openFeatureReason(want["experiment"].Reason), experiment.Reason, identifier)
			reasons[experiment.Reason] = true
			variants[experiment.Variant] = true
		}
		require.Greater(t, len(variants), 1, "the split places identities in more than one variant: %s", strings.Join(keys(variants), ", "))
		require.Contains(t, reasons, "SPLIT", "some identity is placed by the split")
		require.Contains(t, reasons, "STATIC", "some identity falls into the control bucket")
	})
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
