package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// render runs the script on a manifest and returns the rendered manifest, the output of the script and its error.
func render(t *testing.T, manifest string) (rendered, output string, err error) {
	t.Helper()
	if _, lookErr := exec.LookPath("bash"); lookErr != nil {
		t.Skip("the renderer is a bash script")
	}
	dest := filepath.Join(t.TempDir(), "manifest.yaml")
	cmd := exec.Command("bash", ".github/scripts/render-published-manifest.sh", manifest, dest)
	cmd.Dir = root(t)
	out, err := cmd.CombinedOutput()
	data, _ := os.ReadFile(dest)
	return string(data), string(out), err
}

// TestRenderedManifestHasNoReplaces (UC-012 BR-004, Q10): the published manifest of every standard distribution pins the
// placeholder version for each component module and has no replaces.
func TestRenderedManifestHasNoReplaces(t *testing.T) {
	module := regexp.MustCompile(`\{module: [^,}]+, version: ([^,}]+)\}`)
	for _, name := range []string{"flagd-minimal", "flagd-minimal-otel", "flagd-cloud", "flagd-kubernetes", "flagd-sync"} {
		t.Run(name, func(t *testing.T) {
			rendered, out, err := render(t, filepath.Join("distributions", name, "manifest.yaml"))
			require.NoError(t, err, out)
			require.NotContains(t, rendered, "replaces")
			require.NotContains(t, rendered, ", path:")
			versions := module.FindAllStringSubmatch(rendered, -1)
			require.NotEmpty(t, versions)
			for _, v := range versions {
				require.Equal(t, "v0.0.0-dryrun", v[1])
			}
			require.Contains(t, rendered, "name: "+name, "the rest of the manifest stays")
		})
	}
}

// TestRenderedManifestNamesAModuleOutsideTheRepository (UC-012 A2): a manifest that names a module no release of this
// repository tags fails, naming it.
func TestRenderedManifestNamesAModuleOutsideTheRepository(t *testing.T) {
	_, out, err := render(t, filepath.Join("test", "release", "testdata", "unknown-module.yaml"))
	require.Error(t, err)
	require.Contains(t, out, "github.com/example/elsewhere/sync/vendor")
	require.False(t, strings.Contains(out, "sync/file, which"), "the component module is not reported")
}
