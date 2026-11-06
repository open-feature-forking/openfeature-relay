package oci

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/ocitest"
)

// oci-layout.py had no test file of its own: the rows below are the cases its subcommands decide, and the parity test
// (parity_test.go) runs the Python on the same layouts.

const (
	name     = "flagd-minimal"
	revision = "0123456789abcdef0123456789abcdef01234567"
	version  = "main-0123456"
	created  = "2026-01-02T03:04:05Z"
)

func marked() ocitest.LayoutSpec {
	m := map[string]string{}
	for _, p := range Marking(name, revision, version) {
		m[p.Key] = p.Value.(string)
	}
	m[Created] = created
	return ocitest.LayoutSpec{Refs: []string{"ghcr.io/open-feature-forking/flagd-minimal:main", "ghcr.io/open-feature-forking/flagd-minimal:sha-0123456"},
		Annotations: m, Labels: m, AttestationPerPlatform: true}
}

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func checkArgs(layout string, extra ...string) []string {
	return append([]string{"check", layout, "--name", name, "--tags", strings.Join(marked().Refs, ","), "--revision", revision,
		"--version", version, "--created", created}, extra...)
}

// checkCases are the layouts the check command is run on, here and in the parity test.
var checkCases = []struct {
	name      string
	edit      func(*ocitest.LayoutSpec)
	extra     []string
	wantCode  int
	wantLines []string // substrings of the lines that must appear, in the report
}{
	{name: "a marked layout passes every check", wantCode: 0, wantLines: []string{
		"ok: tags of the layout", "ok: platforms", "ok: attestation manifests", "ok: provenance", "ok: SBOM",
		"ok: index annotations", "ok: index created", "ok: amd64: manifest annotations", "ok: arm64: image labels"}},
	{name: "a tag the layout lacks", edit: func(s *ocitest.LayoutSpec) { s.Refs = s.Refs[:1] }, wantCode: 1,
		wantLines: []string{"FAIL: tags of the layout: expected ['ghcr.io/open-feature-forking/flagd-minimal:main', 'ghcr.io/open-feature-forking/flagd-minimal:sha-0123456'], got ['ghcr.io/open-feature-forking/flagd-minimal:main']"}},
	{name: "a platform the layout lacks", edit: func(s *ocitest.LayoutSpec) { s.Platforms = []string{"amd64"} }, wantCode: 1,
		wantLines: []string{"FAIL: platforms: expected ['linux/amd64', 'linux/arm64'], got ['linux/amd64']"}},
	{name: "platforms are an option", edit: func(s *ocitest.LayoutSpec) { s.Platforms = []string{"amd64"} }, extra: []string{"--platforms", "linux/amd64"},
		wantCode: 0, wantLines: []string{"ok: platforms"}},
	{name: "no attestation manifest", edit: func(s *ocitest.LayoutSpec) { s.NoAttestation = true }, wantCode: 1,
		wantLines: []string{"FAIL: attestation manifests: expected 2, got 0", "FAIL: provenance: expected True, got False", "FAIL: SBOM: expected True, got False"}},
	{name: "no provenance", edit: func(s *ocitest.LayoutSpec) { s.Predicates = []string{"https://spdx.dev/Document"} }, wantCode: 1,
		wantLines: []string{"FAIL: provenance: expected True, got False", "ok: SBOM"}},
	{name: "no SBOM", edit: func(s *ocitest.LayoutSpec) { s.Predicates = []string{"https://slsa.dev/provenance/v1"} }, wantCode: 1,
		wantLines: []string{"ok: provenance", "FAIL: SBOM: expected True, got False"}},
	{name: "a wrong revision on the index and the manifests", edit: func(s *ocitest.LayoutSpec) {
		s.Annotations = map[string]string{}
		for k, v := range marked().Annotations {
			s.Annotations[k] = v
		}
		s.Annotations[Revision] = "ffff"
	}, wantCode: 1, wantLines: []string{"FAIL: index annotations:", "'org.opencontainers.image.revision': 'ffff'", "FAIL: amd64: manifest annotations:", "ok: amd64: image labels"}},
	{name: "a missing created annotation", edit: func(s *ocitest.LayoutSpec) {
		s.Annotations = map[string]string{}
		for k, v := range marked().Annotations {
			if k != Created {
				s.Annotations[k] = v
			}
		}
	}, wantCode: 1, wantLines: []string{"FAIL: index created: expected '" + created + "', got None", "ok: index annotations"}},
	{name: "a label that is missing", edit: func(s *ocitest.LayoutSpec) {
		s.Labels = map[string]string{}
		for k, v := range marked().Labels {
			if k != Licenses {
				s.Labels[k] = v
			}
		}
	}, wantCode: 1, wantLines: []string{"FAIL: arm64: image labels:", "'org.opencontainers.image.licenses': None"}},
}

func TestCheck(t *testing.T) {
	for _, tt := range checkCases {
		t.Run(tt.name, func(t *testing.T) {
			spec := marked()
			if tt.edit != nil {
				tt.edit(&spec)
			}
			layout := filepath.Join(t.TempDir(), "layout.tar")
			ocitest.WriteLayout(t, layout, spec)
			code, stdout, stderr := run(t, checkArgs(layout, tt.extra...)...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\n%s%s", code, tt.wantCode, stdout, stderr)
			}
			for _, want := range tt.wantLines {
				if !strings.Contains(stdout, want) {
					t.Errorf("the report lacks %q:\n%s", want, stdout)
				}
			}
			if stderr != "" {
				t.Errorf("stderr = %q", stderr)
			}
		})
	}
}

func TestCheckErrors(t *testing.T) {
	layout := filepath.Join(t.TempDir(), "layout.tar")
	ocitest.WriteLayout(t, layout, marked())
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{"a layout that is not there", checkArgs(filepath.Join(t.TempDir(), "nope.tar")), 1},
		{"a tag the layout does not carry at all", []string{"check", layout, "--name", name, "--tags", "x:y", "--revision", revision, "--version", version, "--created", created}, 1},
		{"a missing required flag", []string{"check", layout, "--name", name}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _, stderr := run(t, tt.args...); code != tt.wantCode || stderr == "" {
				t.Errorf("exit code = %d, stderr %q; want %d with a message", code, stderr, tt.wantCode)
			}
		})
	}
}

func TestMarking(t *testing.T) {
	code, stdout, _ := run(t, "marking", "--name", name, "--revision", revision, "--version", version, "--created", created)
	want := strings.Join([]string{
		"org.opencontainers.image.title=flagd-minimal",
		"org.opencontainers.image.description=Proof of concept from the OpenFeature Relay incubator, built from main. Not an official OpenFeature image; not for production.",
		"org.opencontainers.image.source=https://github.com/open-feature-forking/openfeature-relay",
		"org.opencontainers.image.revision=" + revision,
		"org.opencontainers.image.version=" + version,
		"org.opencontainers.image.licenses=Apache-2.0",
		"org.opencontainers.image.created=" + created,
	}, "\n") + "\n"
	if code != 0 || stdout != want {
		t.Errorf("code %d, stdout:\n%s\nwant:\n%s", code, stdout, want)
	}
}

func TestLoad(t *testing.T) {
	layout := filepath.Join(t.TempDir(), "layout.tar")
	ocitest.WriteLayout(t, layout, marked())
	tag := marked().Refs[0]

	read := func(t *testing.T, args ...string) map[string]string {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := Run(args, &out, &errOut); code != 0 {
			t.Fatalf("exit code %d: %s", code, errOut.String())
		}
		files := map[string]string{}
		tr := tar.NewReader(&out)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(tr)
			files[h.Name] = string(data)
		}
		return files
	}

	for _, arch := range []string{"amd64", "arm64"} {
		t.Run("the "+arch+" image", func(t *testing.T) {
			args := []string{"load", layout, tag}
			if arch != "amd64" {
				args = append(args, "--platform", "linux/"+arch)
			}
			files := read(t, args...)
			var manifest []struct {
				Config   string
				RepoTags []string
				Layers   []string
			}
			if err := json.Unmarshal([]byte(files["manifest.json"]), &manifest); err != nil {
				t.Fatal(err)
			}
			if len(manifest) != 1 || manifest[0].RepoTags[0] != tag || len(manifest[0].Layers) != 1 {
				t.Fatalf("manifest.json = %s", files["manifest.json"])
			}
			if !strings.Contains(files[manifest[0].Config], `"arch":"`+arch+`"`) {
				t.Errorf("the config is not the %s image's: %s", arch, files[manifest[0].Config])
			}
			if files[manifest[0].Layers[0]] != "layer "+arch+" "+filepath.Base(layout) {
				t.Errorf("layer = %q", files[manifest[0].Layers[0]])
			}
			if len(files) != 3 {
				t.Errorf("%d members, want config, layer and manifest.json", len(files))
			}
			wantSpacing := `[{"Config": "` + manifest[0].Config + `", "RepoTags": ["` + tag + `"], "Layers": ["` + manifest[0].Layers[0] + `"]}]`
			if files["manifest.json"] != wantSpacing {
				t.Errorf("manifest.json = %s, want %s", files["manifest.json"], wantSpacing)
			}
		})
	}
	failures := []struct {
		name string
		args []string
	}{
		{"a platform the layout lacks", []string{"load", layout, tag, "--platform", "linux/s390x"}},
		{"a tag the layout lacks", []string{"load", layout, "nope:latest"}},
		{"a platform that is not os/architecture", []string{"load", layout, tag, "--platform", "amd64"}},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			var errOut strings.Builder
			if code := Run(tt.args, &out, &errOut); code != 1 || errOut.Len() == 0 || out.Len() != 0 {
				t.Errorf("exit code %d, stderr %q, %d bytes on stdout", code, errOut.String(), out.Len())
			}
		})
	}
}

func TestPlatforms(t *testing.T) {
	tests := []struct {
		name string
		spec ocitest.LayoutSpec
		want string
	}{
		{"two images and an attestation, one ref", ocitest.LayoutSpec{Refs: []string{"a:b"}}, "linux/amd64 linux/arm64"},
		{"one image", ocitest.LayoutSpec{Refs: []string{"a:b"}, Platforms: []string{"amd64"}}, "linux/amd64"},
		{"sorted", ocitest.LayoutSpec{Refs: []string{"a:b"}, Platforms: []string{"s390x", "arm64", "amd64"}}, "linux/amd64 linux/arm64 linux/s390x"},
		{"two refs: the top-level descriptors are not images", ocitest.LayoutSpec{Refs: []string{"a:b", "a:c"}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout := filepath.Join(t.TempDir(), "layout.tar")
			ocitest.WriteLayout(t, layout, tt.spec)
			code, stdout, stderr := run(t, "platforms", layout)
			if code != 0 || stdout != tt.want+"\n" {
				t.Errorf("code %d, stdout %q, stderr %q, want %q", code, stdout, stderr, tt.want)
			}
		})
	}
}

func TestRunUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"nope"}} {
		if code, _, stderr := run(t, args...); code != 2 || !strings.Contains(stderr, "usage: relay-ci oci") {
			t.Errorf("%v: code %d, stderr %q", args, code, stderr)
		}
	}
	if code, stdout, _ := run(t, "--help"); code != 0 || !strings.Contains(stdout, "usage: relay-ci oci") {
		t.Errorf("help: code %d", code)
	}
}

func TestRepr(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{nil, "None"}, {true, "True"}, {false, "False"}, {3, "3"}, {"a", "'a'"}, {"it's", `"it's"`}, {`a"b'c`, `'a"b\'c'`},
		{"line\nbreak\t\\", `'line\nbreak\t\\'`}, {"é", "'é'"}, {"\x01", `'\x01'`},
		{[]string{}, "[]"}, {[]string{"a", "b"}, "['a', 'b']"},
		{Pairs{{"k", "v"}, {"n", nil}}, "{'k': 'v', 'n': None}"},
		{map[string]any{"b": 1.0, "a": "x"}, "{'a': 'x', 'b': 1}"},
	}
	for _, tt := range tests {
		if got := Repr(tt.in); got != tt.want {
			t.Errorf("Repr(%#v) = %s, want %s", tt.in, got, tt.want)
		}
	}
}
