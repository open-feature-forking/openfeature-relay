// Package image builds the reference Dockerfile's images and checks them (UC-002): the platforms of the multi-platform
// image, the image's identity and user, its components, the FIPS variant and a compile failure for one platform. The
// images are written as OCI layouts or loaded into the local Docker; nothing is pushed (Q6).
package image

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// The same build arguments for every build, so that the layers are shared.
	version = "v9.9.9"
	commit  = "abc1234"
	date    = "2026-01-02T03:04:05Z"

	imageTestEnv = "IMAGE_TEST"
)

var builderName = fmt.Sprintf("relay-image-test-%d", os.Getpid())

// TestMain creates the buildx builder the tests share and removes it: the OCI exporter needs the docker-container
// driver, which the default builder does not have.
func TestMain(m *testing.M) {
	if os.Getenv(imageTestEnv) == "" {
		os.Exit(m.Run())
	}
	create := exec.Command("docker", "buildx", "create", "--name", builderName, "--driver", "docker-container", "--bootstrap")
	if out, err := create.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "creating the buildx builder: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = exec.Command("docker", "buildx", "rm", "--force", builderName).Run()
	os.Exit(code)
}

func requireDocker(t *testing.T) {
	t.Helper()
	if os.Getenv(imageTestEnv) == "" {
		t.Skip("the image tests need Docker with buildx; make image-test sets IMAGE_TEST")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return dir
}

func run(t *testing.T, dir string, env []string, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// generate runs the Builder on the manifest of a repository distribution, without compiling, and returns the source
// directory relative to the repository root, which is the build context (Q5).
func generate(t *testing.T, name string) string {
	t.Helper()
	root := repoRoot(t)
	rel := filepath.ToSlash(filepath.Join("test", "distributions", name, "build"))
	out, err := run(t, filepath.Join(root, "builder"), []string{"GOWORK=off"}, "go", "run", "./cmd/relay-builder",
		"--manifest", filepath.Join(root, "test", "distributions", name, "manifest.yaml"),
		"--skip-compile", "--output", filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err, out)
	return rel
}

func buildArgs(distPath string, fips bool) []string {
	args := []string{"--build-arg", "DIST_PATH=" + distPath, "--build-arg", "VERSION=" + version,
		"--build-arg", "COMMIT=" + commit, "--build-arg", "DATE=" + date}
	if fips {
		args = append(args, "--build-arg", "FIPS=on")
	}
	return args
}

func buildx(t *testing.T, contextDir string, extra ...string) (string, error) {
	t.Helper()
	args := append([]string{"buildx", "build", "--builder", builderName, "--progress=plain", "--provenance=false", "--sbom=false",
		"-f", filepath.Join(repoRoot(t), "builder", "reference", "Dockerfile")}, extra...)
	return run(t, contextDir, nil, "docker", append(args, contextDir)...)
}

// loadLocal builds the image for the local platform into the local Docker (A2) and returns its tag.
func loadLocal(t *testing.T, name, distPath string, fips bool) string {
	t.Helper()
	requireDocker(t)
	tag := "relay-image-test/" + name + ":standard"
	if fips {
		tag = "relay-image-test/" + name + ":fips"
	}
	out, err := buildx(t, repoRoot(t), append(buildArgs(distPath, fips), "--platform", "linux/"+runtime.GOARCH, "--load", "-t", tag)...)
	require.NoError(t, err, out)
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "--force", tag).Run() })
	return tag
}

// ociPlatforms builds the image for the platforms into an OCI layout and returns the platforms its index lists.
func ociPlatforms(t *testing.T, contextDir string, extra []string, platforms ...string) []string {
	t.Helper()
	layout := filepath.Join(t.TempDir(), "image.tar")
	out, err := buildx(t, contextDir, append(extra, "--platform", strings.Join(platforms, ","), "--output", "type=oci,dest="+layout)...)
	require.NoError(t, err, out)
	return indexPlatforms(t, layout)
}

// indexPlatforms reads the platforms of the multi-platform image in an OCI layout archive.
func indexPlatforms(t *testing.T, layout string) []string {
	t.Helper()
	f, err := os.Open(layout)
	require.NoError(t, err)
	defer f.Close()
	files := map[string][]byte{}
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if hdr.Typeflag != tar.TypeReg || hdr.Size > 1<<20 {
			continue
		}
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		files[hdr.Name] = data
	}
	type descriptor struct {
		Digest   string `json:"digest"`
		Platform *struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		} `json:"platform"`
	}
	type index struct {
		Manifests []descriptor `json:"manifests"`
	}
	var top index
	require.NoError(t, json.Unmarshal(files["index.json"], &top))
	require.Len(t, top.Manifests, 1, "the layout holds one image")
	blob := files["blobs/"+strings.Replace(top.Manifests[0].Digest, ":", "/", 1)]
	var multi index
	require.NoError(t, json.Unmarshal(blob, &multi))
	var platforms []string
	for _, m := range multi.Manifests {
		require.NotNil(t, m.Platform, "every manifest of the index names its platform")
		platforms = append(platforms, m.Platform.OS+"/"+m.Platform.Architecture)
	}
	sort.Strings(platforms)
	return platforms
}

// fromLines returns the FROM lines of a Dockerfile.
func fromLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "FROM ") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}

// TestReferenceDockerfilePins (UC-002 BR-005, BR-006): the reference Dockerfile builds on flagd's two pinned digests,
// and no image of this repository is used for the runtime. A digest bumped in one Dockerfile and not the other fails.
func TestReferenceDockerfilePins(t *testing.T) {
	root := repoRoot(t)
	reference := fromLines(t, filepath.Join(root, "builder", "reference", "Dockerfile"))
	require.Len(t, reference, 2)
	require.Equal(t, fromLines(t, filepath.Join(root, "flagd", "build.Dockerfile")), reference)
	for _, line := range reference {
		require.Regexp(t, regexp.MustCompile(`@sha256:[0-9a-f]{64}( |$)`), line, "pinned by digest")
	}
	data, err := os.ReadFile(filepath.Join(root, "builder", "reference", "Dockerfile"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "ghcr.io/open-feature")
}

// TestTestbedDockerfilePins (step 17): every FROM of the testbed image, which is published, carries a digest.
func TestTestbedDockerfilePins(t *testing.T) {
	root := repoRoot(t)
	from := fromLines(t, filepath.Join(root, "distributions", "flagd-testbed", "Dockerfile"))
	require.Len(t, from, 3)
	for _, line := range from {
		require.Regexp(t, regexp.MustCompile(`@sha256:[0-9a-f]{64}( |$)`), line, "pinned by digest")
	}
}

// TestReferenceImageMultiPlatform (UC-002 main 2-6, BR-001, A1): the image's index lists every target platform, the
// default ones and a further one.
func TestReferenceImageMultiPlatform(t *testing.T) {
	requireDocker(t)
	for name, platforms := range map[string][]string{
		"example":       {"linux/amd64", "linux/arm64"},
		"minimal-shape": {"linux/amd64", "linux/arm64", "linux/s390x"},
	} {
		t.Run(name, func(t *testing.T) {
			distPath := generate(t, name)
			got := ociPlatforms(t, repoRoot(t), buildArgs(distPath, false), platforms...)
			want := append([]string(nil), platforms...)
			sort.Strings(want)
			require.Equal(t, want, got)
		})
	}
}

// distributions are the version command of each distribution and what it prints: flagd's command line answers
// version --verbose with the distribution's name, the example's own one answers version.
var distributions = map[string]struct {
	versionArgs []string
	want        []string
}{
	"example": {[]string{"version"}, []string{"example " + version + " (" + commit + "), built at: " + date}},
	"minimal-shape": {[]string{"version", "--verbose"}, []string{
		"flagd: " + version + " (" + commit + "), built at: " + date, "distribution: minimal-shape " + version}},
}

// TestReferenceImageRuns (UC-002 main 9, A2, BR-003, postcondition 3): the image loads locally, runs as 65532:65532
// from /flagd-build, has no shell, and reports the name, version, commit and date it was built with.
func TestReferenceImageRuns(t *testing.T) {
	requireDocker(t)
	for name, d := range distributions {
		t.Run(name, func(t *testing.T) {
			tag := loadLocal(t, name, generate(t, name), false)

			out, err := run(t, "", nil, "docker", "image", "inspect", "--format", "{{.Config.User}} {{json .Config.Entrypoint}} {{json .Config.Cmd}}", tag)
			require.NoError(t, err, out)
			require.Equal(t, `65532:65532 ["/flagd-build"] null`, strings.TrimSpace(out))

			out, err = run(t, "", nil, "docker", append([]string{"run", "--rm", tag}, d.versionArgs...)...)
			require.NoError(t, err, out)
			for _, line := range d.want {
				require.Contains(t, out, line)
			}

			out, err = run(t, "", nil, "docker", "run", "--rm", "--entrypoint", "/bin/sh", tag, "-c", "true")
			require.Error(t, err, "the image has no shell: %s", out)
		})
	}
}

// binaryName is the dist.binary of a manifest, flagd when it names none.
func binaryName(t *testing.T, manifest string) string {
	t.Helper()
	data, err := os.ReadFile(manifest)
	require.NoError(t, err)
	if m := regexp.MustCompile(`(?m)^\s+binary:\s*(\S+)`).FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return "flagd"
}

// moduleLines returns the dependency and replacement lines of go version -m, which name the modules a binary links.
func moduleLines(t *testing.T, binary string) []string {
	t.Helper()
	out, err := run(t, "", nil, "go", "version", "-m", binary)
	require.NoError(t, err, out)
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 && (f[0] == "dep" || f[0] == "=>") {
			lines = append(lines, strings.Join(f, " "))
		}
	}
	require.NotEmpty(t, lines)
	return lines
}

// copyOut copies the binary out of an image, for the local platform.
func copyOut(t *testing.T, tag string) string {
	t.Helper()
	id, err := run(t, "", nil, "docker", "create", tag)
	require.NoError(t, err, id)
	id = strings.TrimSpace(id)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", id).Run() })
	dest := filepath.Join(t.TempDir(), "flagd-build")
	out, err := run(t, "", nil, "docker", "cp", id+":/flagd-build", dest)
	require.NoError(t, err, out)
	return dest
}

// TestImageSameComponentsAsBinary (UC-002 BR-002, postcondition 2): the binary in the image links the same modules, at
// the same versions, as the binary the Builder compiles from the same manifest.
func TestImageSameComponentsAsBinary(t *testing.T) {
	requireDocker(t)
	for name := range distributions {
		t.Run(name, func(t *testing.T) {
			root := repoRoot(t)
			dir := filepath.Join(root, "test", "distributions", name)
			out, err := run(t, filepath.Join(root, "builder"), []string{"GOWORK=off"}, "go", "run", "./cmd/relay-builder",
				"--manifest", filepath.Join(dir, "manifest.yaml"), "--output", filepath.Join(dir, "build"),
				"--dist-version", version, "--commit", commit, "--date", date)
			require.NoError(t, err, out)
			binary := filepath.Join(dir, "build", binaryName(t, filepath.Join(dir, "manifest.yaml")))
			require.FileExists(t, binary, "the Builder compiled a binary")

			tag := loadLocal(t, name, filepath.ToSlash(filepath.Join("test", "distributions", name, "build")), false)
			require.Equal(t, moduleLines(t, binary), moduleLines(t, copyOut(t, tag)))
		})
	}
}

// TestReferenceImageFIPS (UC-002 A3, BR-007): the FIPS variant is built against the certified module, and says so.
func TestReferenceImageFIPS(t *testing.T) {
	requireDocker(t)
	tag := loadLocal(t, "minimal-shape", generate(t, "minimal-shape"), true)

	out, err := run(t, "", nil, "go", "version", "-m", copyOut(t, tag))
	require.NoError(t, err, out)
	require.Contains(t, out, "-tags=fips140,fips140v1.0")

	out, err = run(t, "", nil, "docker", "run", "--rm", tag, "version")
	require.NoError(t, err, out)
	require.Contains(t, out, "build variant: fips")
}

// TestPlatformCompileFailureBuildsNoImage (UC-002 A4, BR-004): a distribution that does not compile for one target
// platform fails the whole build, names the platform, and leaves nothing at the destination.
func TestPlatformCompileFailureBuildsNoImage(t *testing.T) {
	requireDocker(t)
	src := t.TempDir()
	for _, name := range []string{"go.mod", "main.go", "broken_arm64.go"} {
		data, err := os.ReadFile(filepath.Join("fixtures", "arm64-broken", name+".fixture"))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(src, name), data, 0o600))
	}
	layout := filepath.Join(t.TempDir(), "image.tar")
	out, err := buildx(t, src, append(buildArgs(".", false), "--platform", "linux/amd64,linux/arm64", "--output", "type=oci,dest="+layout)...)
	require.Error(t, err, out)
	require.Contains(t, out, "linux/arm64")
	require.NoFileExists(t, layout)
}
