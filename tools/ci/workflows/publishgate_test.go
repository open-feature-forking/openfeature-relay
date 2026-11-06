package workflows

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The port of test_publish_gate.py: publish-gate.sh decides whether a run builds and publishes the images: the nightly
// run and a manual run on main (step 17).

const repository = "open-feature-forking/openfeature-relay"

// gate runs publish-gate.sh with the defaults of a nightly run on main, overridden by env, and returns its output lines.
func gate(t *testing.T, env map[string]string) map[string]string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		skip(t, "needs bash")
	}
	vars := map[string]string{"EVENT": "schedule", "REF": "refs/heads/main", "REPOSITORY": repository, "FULL_CI": "", "INPUTS_CHANGED": "true"}
	maps.Copy(vars, env)
	cmd := exec.Command(bash, filepath.Join(root(t), ".github", "scripts", "publish-gate.sh"))
	cmd.Env = os.Environ()
	for k, v := range vars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("publish-gate.sh: %v", err)
	}
	got := map[string]string{}
	for _, line := range strings.Fields(string(out)) {
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.Contains(v, "=") {
			t.Fatalf("output line %q is not key=value", line)
		}
		got[k] = v
	}
	return got
}

func assertGate(t *testing.T, want map[string]string, env map[string]string) {
	t.Helper()
	if got := gate(t, env); !maps.Equal(want, got) {
		t.Errorf("gate(%v) = %v, want %v", env, got, want)
	}
}

func TestTheNightlyRunPublishesWhenImageInputsChanged(t *testing.T) {
	assertGate(t, map[string]string{"build": "true", "publish": "true"}, nil)
}

func TestTheNightlyRunDoesNotPublishWhenNothingChanged(t *testing.T) {
	assertGate(t, map[string]string{"build": "false", "publish": "false"}, map[string]string{"INPUTS_CHANGED": "false"})
}

func TestAPushNeverPublishesOrBuilds(t *testing.T) {
	assertGate(t, map[string]string{"build": "false", "publish": "false"}, map[string]string{"EVENT": "push"})
}

func TestADispatchOnMainPublishesAtOnce(t *testing.T) {
	assertGate(t, map[string]string{"build": "true", "publish": "true"},
		map[string]string{"EVENT": "workflow_dispatch", "INPUTS_CHANGED": "false"})
}

func TestADispatchOnABranchBuildsAndDoesNotPublish(t *testing.T) {
	assertGate(t, map[string]string{"build": "true", "publish": "false"},
		map[string]string{"EVENT": "workflow_dispatch", "REF": "refs/heads/step-17/pr-b"})
}

func TestAPullRequestABranchAndAForkNeverPublish(t *testing.T) {
	for _, env := range []map[string]string{
		{"EVENT": "pull_request", "REF": "refs/pull/1/merge"}, {"REF": "refs/heads/feature/workflows"},
		{"REPOSITORY": "someone/openfeature-relay"}, {"EVENT": "workflow_dispatch", "REPOSITORY": "someone/openfeature-relay"}} {
		t.Run(env["EVENT"]+env["REF"]+env["REPOSITORY"], func(t *testing.T) {
			if got := gate(t, env)["publish"]; got != "false" {
				t.Errorf("publish = %q, want false", got)
			}
		})
	}
}

func TestFullCIBuildsAndNeverPublishesByItself(t *testing.T) {
	assertGate(t, map[string]string{"build": "true", "publish": "false"},
		map[string]string{"EVENT": "pull_request", "REF": "refs/pull/1/merge", "FULL_CI": "true"})
}
