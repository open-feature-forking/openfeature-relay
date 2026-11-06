package distributions

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// testbedModules are the modules of distributions/flagd-testbed besides the runtime, each with the suite that needs it
// (UC-022 BR-005, Q15). A module added to the manifest without a suite that needs it shows up as a diff here.
var testbedModules = []string{
	relay + "configformat/flagd",      // every source's flag configuration
	relay + "evaluator/flagd",         // TestRPC: every evaluation through port 8013
	relay + "runtime",                 // the runtime
	relay + "service/flag-evaluation", // TestRPC and the rpc.service route of envoy; and the deprecated services it calls
	relay + "service/flag-evaluation-legacy", // TestRPC: the deprecated services of port 8013
	relay + "service/flag-sync",       // TestInProcess and the sync.service route of envoy
	relay + "sync/file",               // selector-flags.json and the metadata configuration's source
	relay + "testbed/cmd",             // the control API and the flagd target
	relay + "testbed/source",          // the combined flags that flags/allFlags.json named
}

// TestTestbedManifestIsMinimal pins the testbed's component list, and that it has no telemetry, no middleware, no event
// sink, no OFREP, no event collector and no other sync provider, and flagd's error texts.
func TestTestbedManifestIsMinimal(t *testing.T) {
	path := manifestOf(t, "flagd-testbed")
	m := readManifest(t, path)

	got := m.selected()
	sort.Strings(got)
	want := append([]string(nil), testbedModules...)
	sort.Strings(want)
	require.Equal(t, want, got)
	require.Nil(t, m.Telemetry, "noop telemetry (Q4)")
	require.Empty(t, m.Middleware)
	require.Empty(t, m.EventSinks)
	require.Empty(t, m.Engines)
	require.NotNil(t, m.Cmd)
	require.Equal(t, relay+"testbed/cmd", m.Cmd.Module)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var raw struct {
		Dist struct {
			ComponentsHint *bool `yaml:"components_hint"`
		} `yaml:"dist"`
	}
	require.NoError(t, yaml.Unmarshal(data, &raw))
	require.NotNil(t, raw.Dist.ComponentsHint)
	require.False(t, *raw.Dist.ComponentsHint, "the suites check flagd's error texts")
}

// TestStandardManifestsHaveNoTestbedModule pins NFR-015: no standard distribution lists or replaces a testbed module.
func TestStandardManifestsHaveNoTestbedModule(t *testing.T) {
	names := []string{"flagd-full"}
	for _, d := range standard {
		names = append(names, d.name)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			m := readManifest(t, manifestOf(t, name))
			for _, module := range m.selected() {
				require.NotContains(t, module, "/testbed/", "%s lists a testbed module", name)
			}
			for _, r := range m.Replaces {
				require.NotContains(t, r.Module, "/testbed/", "%s replaces a testbed module", name)
			}
		})
	}
}

// TestImageDataMatchesSubmodule pins BR-007: the image takes its flags, configurations and certificate data from the
// test-harness submodule as they are, and the Dockerfile changes none of it.
func TestImageDataMatchesSubmodule(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join(root(t), "distributions", "flagd-testbed", "Dockerfile"))
	require.NoError(t, err)

	var sources []string
	for _, line := range strings.Split(string(dockerfile), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "COPY" && !strings.HasPrefix(fields[1], "--from") {
			sources = append(sources, fields[1:len(fields)-1]...)
		}
	}
	sort.Strings(sources)
	require.Equal(t, []string{"test-harness/flags/*", "test-harness/launchpad/configs", "test-harness/ssl/*"}, sources)

	// nothing edits the copied data: no step of the Dockerfile names it besides mkdir of the volume
	touches := regexp.MustCompile(`(?m)^RUN .*(rawflags|configs|sed |echo .*>)`)
	require.Empty(t, touches.FindAllString(string(dockerfile), -1))

	for _, dir := range []string{"flags", "launchpad/configs", "ssl"} {
		entries, err := os.ReadDir(filepath.Join(root(t), "test-harness", filepath.FromSlash(dir)))
		require.NoError(t, err, "the test harness is missing: git submodule update --init test-harness")
		require.NotEmpty(t, entries, dir)
	}
}
