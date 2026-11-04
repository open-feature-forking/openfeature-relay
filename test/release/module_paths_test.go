package release

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const repositoryPath = "github.com/open-feature-forking/openfeature-relay"

// notComponents are the directories whose modules a release tag never names: the tests and the test harness, the
// distributions' generated builds, and the two binaries that keep the upstream flagd module paths.
var notComponents = map[string]bool{
	"test": true, "test-harness": true, "spec": true, "schemas": true, "playground-app": true, "distributions": true,
	"flagd": true, "flagd-proxy": true, "docs": true, "samples": true, "images": true,
}

var modulePattern = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// root returns the repository root, two directories above this test.
func root(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// mismatches returns one message per component module under dir whose path is not repositoryPath/<its directory>, and
// so would not resolve from the tag <its directory>/vX.Y.Z.
func mismatches(t *testing.T, dir string) (checked int, bad []string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, filepath.Dir(path))
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".claude" || (filepath.Dir(path) == dir && notComponents[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" || rel == "." {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		match := modulePattern.FindSubmatch(data)
		if match == nil {
			bad = append(bad, fmt.Sprintf("%s/go.mod declares no module", rel))
		} else if want := repositoryPath + "/" + rel; string(match[1]) != want {
			bad = append(bad, fmt.Sprintf("module %s in %s: the tag %s/vX.Y.Z resolves to %s", match[1], rel, rel, want))
		}
		return nil
	}))
	sort.Strings(bad)
	return checked, bad
}

// TestModulePathsMatchTagDirectories pins BR-001 (UC-012 A4): a component module is released by a tag named after its
// directory, <dir>/vX.Y.Z, and the go command resolves that tag only when the module path is the repository path
// followed by <dir>.
func TestModulePathsMatchTagDirectories(t *testing.T) {
	checked, bad := mismatches(t, root(t))
	require.NotZero(t, checked, "no component module found: the walk is broken")
	require.Empty(t, bad, strings.Join(bad, "\n"))
}

func TestModulePathMismatchNamesTheModule(t *testing.T) {
	dir := t.TempDir()
	for sub, module := range map[string]string{
		"sync/good":  repositoryPath + "/sync/good",
		"sync/wrong": "github.com/open-feature/flagd/sync/wrong",
		"flagd":      "github.com/open-feature/flagd/flagd",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, sub), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, sub, "go.mod"), []byte("module "+module+"\n"), 0o644))
	}
	checked, bad := mismatches(t, dir)
	require.Equal(t, 2, checked)
	require.Len(t, bad, 1)
	require.Contains(t, bad[0], "github.com/open-feature/flagd/sync/wrong")
	require.Contains(t, bad[0], "sync/wrong")
}
