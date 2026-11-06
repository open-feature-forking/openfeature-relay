package oci

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/flagargs"
)

const usage = `usage: relay-ci oci <command> [arguments]

  check <layout.tar> --name N --tags T1,T2 --revision R --version V --created C [--platforms P1,P2]
      Prints one "ok: ..." or "FAIL: ..." line per check and exits 1 on any FAIL.
  marking --name N --revision R --version V --created C
      Prints the marking as key=value lines, for the --label and --annotation arguments of a build.
  load <layout.tar> <tag> [--platform linux/amd64]
      Writes a docker-archive of one platform's image to stdout, for docker load.
  platforms <layout.tar>
      Prints the platforms of the images in a layout, sorted and separated by spaces.
`

// Run runs one `relay-ci oci` command and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	var (
		spec flagargs.Spec
		run  func(flagargs.Result, io.Writer) error
	)
	switch args[0] {
	case "check":
		spec = flagargs.Spec{Values: []string{"name", "tags", "revision", "version", "created", "platforms"},
			Required: []string{"name", "tags", "revision", "version", "created"}, Positionals: 1}
		run = func(r flagargs.Result, out io.Writer) error { return runCheck(r, out) }
	case "marking":
		spec = flagargs.Spec{Values: []string{"name", "revision", "version", "created"},
			Required: []string{"name", "revision", "version", "created"}}
		run = func(r flagargs.Result, out io.Writer) error { return runMarking(r, out) }
	case "load":
		spec = flagargs.Spec{Values: []string{"platform"}, Positionals: 2}
		run = func(r flagargs.Result, out io.Writer) error { return runLoad(r, out) }
	case "platforms":
		spec = flagargs.Spec{Positionals: 1}
		run = func(r flagargs.Result, out io.Writer) error { return runPlatforms(r, out) }
	case "help", "-h", "--help":
		_, _ = io.WriteString(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "relay-ci oci: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	parsed, err := flagargs.Parse(args[1:], spec)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "relay-ci oci %s: %v\n", args[0], err)
		return 2
	}
	if err := run(parsed, stdout); err != nil {
		if err != errChecksFailed {
			_, _ = fmt.Fprintf(stderr, "relay-ci oci %s: %v\n", args[0], err)
		}
		return 1
	}
	return 0
}

// errChecksFailed is returned by check when a line printed FAIL; the lines are the report, so nothing more is printed.
var errChecksFailed = errors.New("a check failed")

func runMarking(r flagargs.Result, out io.Writer) error {
	pairs := append(Marking(r.Value("name"), r.Value("revision"), r.Value("version")), Pair{Created, r.Value("created")})
	for _, p := range pairs {
		if _, err := fmt.Fprintf(out, "%s=%s\n", p.Key, p.Value); err != nil {
			return err
		}
	}
	return nil
}

func runCheck(r flagargs.Result, out io.Writer) error {
	failures := 0
	result := func(what string, want, got any) {
		if Repr(want) == Repr(got) {
			_, _ = fmt.Fprintf(out, "ok: %s\n", what)
			return
		}
		failures++
		_, _ = fmt.Fprintf(out, "FAIL: %s: expected %s, got %s\n", what, Repr(want), Repr(got))
	}

	platformsArg := "linux/amd64,linux/arm64"
	if r.Given("platforms") {
		platformsArg = r.Value("platforms")
	}
	created := r.Value("created")
	layout, err := OpenLayout(r.Positional[0])
	if err != nil {
		return err
	}
	defer layout.Close()
	tags := strings.Split(r.Value("tags"), ",")
	sort.Strings(tags)
	refs, err := layout.Refs()
	if err != nil {
		return err
	}
	have := make([]string, 0, len(refs))
	for ref := range refs {
		have = append(have, ref)
	}
	sort.Strings(have)
	result("tags of the layout", tags, have)
	index, err := layout.Index(tags[0])
	if err != nil {
		return err
	}
	var platforms []string
	for _, m := range PlatformManifests(index) {
		platforms = append(platforms, *m.Platform.OS+"/"+m.Platform.Architecture)
	}
	sort.Strings(platforms)
	want := strings.Split(platformsArg, ",")
	sort.Strings(want)
	result("platforms", want, platforms)

	attestations := Attestations(index)
	result("attestation manifests", len(platforms), len(attestations))
	var provenance, sbom bool
	for _, a := range attestations {
		var manifest Manifest
		if err := layout.Blob(a.Digest, &manifest); err != nil {
			return err
		}
		for _, layer := range manifest.Layers {
			kind, _ := layer.Annotations["in-toto.io/predicate-type"].(string)
			provenance = provenance || strings.Contains(kind, "slsa.dev/provenance")
			sbom = sbom || strings.Contains(kind, "spdx")
		}
	}
	result("provenance", true, provenance)
	result("SBOM", true, sbom)

	marking := Marking(r.Value("name"), r.Value("revision"), r.Value("version"))
	pick := func(from map[string]any, keys Pairs) Pairs {
		got := make(Pairs, len(keys))
		for i, p := range keys {
			got[i] = Pair{p.Key, from[p.Key]}
		}
		return got
	}
	result("index annotations", marking, pick(index.Annotations, marking))
	result("index created", created, index.Annotations[Created])
	for _, m := range PlatformManifests(index) {
		arch := m.Platform.Architecture
		var manifest Manifest
		if err := layout.Blob(m.Digest, &manifest); err != nil {
			return err
		}
		result(arch+": manifest annotations", marking, pick(manifest.Annotations, marking))
		var cfg ImageConfig
		if err := layout.Blob(manifest.Config.Digest, &cfg); err != nil {
			return err
		}
		wantLabels := append(append(Pairs{}, marking...), Pair{Created, created})
		result(arch+": image labels", wantLabels, pick(cfg.Config.Labels, wantLabels))
	}
	if failures > 0 {
		return errChecksFailed
	}
	return nil
}

func runPlatforms(r flagargs.Result, out io.Writer) error {
	layout, err := OpenLayout(r.Positional[0])
	if err != nil {
		return err
	}
	defer layout.Close()
	raw, err := layout.Raw("index.json")
	if err != nil {
		return err
	}
	var top Index
	if err := json.Unmarshal(raw, &top); err != nil {
		return err
	}
	manifests := top.Manifests
	if len(manifests) == 1 {
		blob, err := layout.RawBlob(manifests[0].Digest)
		if err != nil {
			return err
		}
		var probe struct {
			Manifests *[]Descriptor `json:"manifests"`
		}
		if err := json.Unmarshal(blob, &probe); err != nil {
			return err
		}
		if probe.Manifests != nil {
			manifests = *probe.Manifests
		}
	}
	platforms := []string{}
	for _, m := range PlatformManifests(Index{Manifests: manifests}) {
		platforms = append(platforms, *m.Platform.OS+"/"+m.Platform.Architecture)
	}
	sort.Strings(platforms)
	_, err = fmt.Fprintln(out, strings.Join(platforms, " "))
	return err
}

func runLoad(r flagargs.Result, out io.Writer) error {
	platform := "linux/amd64"
	if r.Given("platform") {
		platform = r.Value("platform")
	}
	osName, arch, ok := strings.Cut(platform, "/")
	if !ok || strings.Contains(arch, "/") {
		return fmt.Errorf("--platform %q is not os/architecture", platform)
	}
	tag := r.Positional[1]
	layout, err := OpenLayout(r.Positional[0])
	if err != nil {
		return err
	}
	defer layout.Close()
	index, err := layout.Index(tag)
	if err != nil {
		return err
	}
	var chosen []Descriptor
	for _, m := range PlatformManifests(index) {
		if *m.Platform.OS == osName && m.Platform.Architecture == arch {
			chosen = append(chosen, m)
		}
	}
	if len(chosen) != 1 {
		return fmt.Errorf("the layout has no single %s image", platform)
	}
	var manifest Manifest
	if err := layout.Blob(chosen[0].Digest, &manifest); err != nil {
		return err
	}
	config, err := BlobName(manifest.Config.Digest)
	if err != nil {
		return err
	}
	layers := make([]string, 0, len(manifest.Layers))
	for _, l := range manifest.Layers {
		name, err := BlobName(l.Digest)
		if err != nil {
			return err
		}
		layers = append(layers, name)
	}
	// Everything is checked before the first byte: a missing member must not leave a half-written archive.
	names := append([]string{config}, layers...)
	for _, name := range names {
		if _, ok := layout.entries[name]; !ok {
			return fmt.Errorf("%s: not in the layout", name)
		}
	}
	tw := tar.NewWriter(out)
	for _, name := range names {
		h := layout.entries[name].header
		if err := tw.WriteHeader(&h); err != nil {
			return err
		}
		src, _ := layout.Open(name)
		if _, err := io.Copy(tw, src); err != nil {
			return err
		}
	}
	body, err := archiveManifest(config, tag, layers)
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Size: int64(len(body)), Mode: 0o644, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write(body); err != nil {
		return err
	}
	return tw.Close()
}

// archiveManifest is the manifest.json of a docker-archive, spaced the way Python's json.dumps writes it.
func archiveManifest(config, tag string, layers []string) ([]byte, error) {
	enc := func(v any) (string, error) {
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		if err := e.Encode(v); err != nil {
			return "", err
		}
		return strings.TrimSuffix(b.String(), "\n"), nil
	}
	c, err := enc(config)
	if err != nil {
		return nil, err
	}
	t, err := enc(tag)
	if err != nil {
		return nil, err
	}
	parts := make([]string, len(layers))
	for i, l := range layers {
		if parts[i], err = enc(l); err != nil {
			return nil, err
		}
	}
	return []byte(fmt.Sprintf(`[{"Config": %s, "RepoTags": [%s], "Layers": [%s]}]`, c, t, strings.Join(parts, ", "))), nil
}
