package oci

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/ocitest"
)

// Parity: until the cutover (slice X) the Python and the Go both exist, so this runs the Python helper and the Go on the
// same synthetic layouts and compares exit code and stdout. It skips without python3, and fails on a skip in
// GitHub Actions.

func TestParityCheckAndMarking(t *testing.T) {
	script := ocitest.Script("oci-layout.py")
	for _, tt := range checkCases {
		t.Run("check: "+tt.name, func(t *testing.T) {
			spec := marked()
			if tt.edit != nil {
				tt.edit(&spec)
			}
			layout := filepath.Join(t.TempDir(), "layout.tar")
			ocitest.WriteLayout(t, layout, spec)
			args := checkArgs(layout, tt.extra...)
			pyCode, pyOut, _ := ocitest.RunPython(t, "", nil, "", append([]string{script}, args...)...)
			goCode, goOut, _ := run(t, args...)
			if pyCode != goCode || pyOut != goOut {
				t.Errorf("python: exit %d\n%s\ngo: exit %d\n%s", pyCode, pyOut, goCode, goOut)
			}
		})
	}
	t.Run("marking", func(t *testing.T) {
		args := []string{"marking", "--name", name, "--revision", revision, "--version", version, "--created", created}
		pyCode, pyOut, _ := ocitest.RunPython(t, "", nil, "", append([]string{script}, args...)...)
		goCode, goOut, _ := run(t, args...)
		if pyCode != goCode || pyOut != goOut {
			t.Errorf("python: exit %d\n%s\ngo: exit %d\n%s", pyCode, pyOut, goCode, goOut)
		}
	})
	t.Run("check: a tag the layout does not carry exits 1 in both", func(t *testing.T) {
		layout := filepath.Join(t.TempDir(), "layout.tar")
		ocitest.WriteLayout(t, layout, marked())
		args := []string{"check", layout, "--name", name, "--tags", "x:y", "--revision", revision, "--version", version, "--created", created}
		pyCode, _, _ := ocitest.RunPython(t, "", nil, "", append([]string{script}, args...)...)
		goCode, _, _ := run(t, args...)
		if pyCode != 1 || goCode != 1 {
			t.Errorf("python exit %d, go exit %d", pyCode, goCode)
		}
	})
}

// members reads a docker-archive into name -> content, and the names in order.
func members(t *testing.T, archive string) (map[string]string, []string) {
	t.Helper()
	files := map[string]string{}
	var order []string
	tr := tar.NewReader(strings.NewReader(archive))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return files, order
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(tr)
		files[h.Name] = string(data)
		order = append(order, h.Name)
	}
}

func TestParityLoad(t *testing.T) {
	script := ocitest.Script("oci-layout.py")
	layout := filepath.Join(t.TempDir(), "layout.tar")
	ocitest.WriteLayout(t, layout, marked())
	tag := marked().Refs[0]
	for _, extra := range [][]string{nil, {"--platform", "linux/arm64"}, {"--platform", "linux/s390x"}} {
		t.Run(strings.Join(append([]string{"load"}, extra...), " "), func(t *testing.T) {
			args := append([]string{"load", layout, tag}, extra...)
			pyCode, pyOut, _ := ocitest.RunPython(t, "", nil, "", append([]string{script}, args...)...)
			goCode, goOut, _ := run(t, args...)
			if pyCode != goCode {
				t.Fatalf("python exit %d, go exit %d", pyCode, goCode)
			}
			pyFiles, pyOrder := members(t, pyOut)
			goFiles, goOrder := members(t, goOut)
			if strings.Join(pyOrder, "\n") != strings.Join(goOrder, "\n") {
				t.Errorf("members: python %v, go %v", pyOrder, goOrder)
			}
			for n, content := range pyFiles {
				if goFiles[n] != content {
					t.Errorf("%s differs:\npython %q\ngo     %q", n, content, goFiles[n])
				}
			}
		})
	}
}

// The `platforms` command replaces a heredoc of release-dry-run.sh: the parity test runs that very text.
func TestParityPlatforms(t *testing.T) {
	text, err := os.ReadFile(ocitest.Script("release-dry-run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)python3 - "\$\{oci\}" <<'PY'\n(.*?)\nPY\n`).FindSubmatch(bytes.ReplaceAll(text, []byte("\r\n"), []byte("\n")))
	if m == nil {
		t.Skip("the heredoc is gone: the dry run already calls relay-ci oci platforms")
	}
	for _, tt := range []struct {
		name string
		spec ocitest.LayoutSpec
	}{
		{"two images and an attestation", ocitest.LayoutSpec{Refs: []string{"a:b"}}},
		{"one image", ocitest.LayoutSpec{Refs: []string{"a:b"}, Platforms: []string{"amd64"}}},
		{"unsorted", ocitest.LayoutSpec{Refs: []string{"a:b"}, Platforms: []string{"s390x", "arm64", "amd64"}}},
		{"two refs", ocitest.LayoutSpec{Refs: []string{"a:b", "a:c"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			layout := filepath.Join(t.TempDir(), "layout.tar")
			ocitest.WriteLayout(t, layout, tt.spec)
			pyCode, pyOut, _ := ocitest.RunPython(t, "", nil, string(m[1])+"\n", "-", layout)
			goCode, goOut, _ := run(t, "platforms", layout)
			if pyCode != goCode || pyOut != goOut {
				t.Errorf("python: exit %d %q, go: exit %d %q", pyCode, pyOut, goCode, goOut)
			}
		})
	}
}
