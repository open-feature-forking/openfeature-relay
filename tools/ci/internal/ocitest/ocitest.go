// Package ocitest holds the fixtures of the tests of internal/oci and internal/publish: synthetic OCI layouts and an
// in-process registry. Nothing here talks to a real registry, and no test needs Docker.
package ocitest

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Media types of the fixtures.
const (
	Index    = "application/vnd.oci.image.index.v1+json"
	Manifest = "application/vnd.oci.image.manifest.v1+json"
)

// Digest returns the sha256 digest of data.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// LayoutSpec describes a synthetic layout: an amd64 and an arm64 image and one attestation manifest, under the refs.
type LayoutSpec struct {
	Refs     []string
	Revision string // the revision annotation of the index, as the publish fixtures set it

	// Platforms are the architectures of the images; nil means amd64 and arm64.
	Platforms []string
	// AttestationPerPlatform adds one attestation manifest per image instead of one in all, as buildx writes.
	AttestationPerPlatform bool
	// MutateIndex edits the image index before it is written, to build a layout a hostile builder could write.
	MutateIndex func(index map[string]any)
	// NoAttestation leaves the attestation manifest out.
	NoAttestation bool
	// Predicates are the predicate types of the attestation manifest's layers; nil means provenance and SPDX.
	Predicates []string
	// Annotations go on the index and on every platform manifest; Labels go into every image configuration.
	Annotations map[string]string
	Labels      map[string]string
}

func marshal(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// WriteLayout writes the layout as a tar file at path and returns the digest of its index.
func WriteLayout(t testing.TB, path string, spec LayoutSpec) string {
	t.Helper()
	blobs := map[string][]byte{}
	var order []string
	blob := func(data []byte) string {
		d := Digest(data)
		name := "blobs/sha256/" + strings.TrimPrefix(d, "sha256:")
		if _, ok := blobs[name]; !ok {
			order = append(order, name)
		}
		blobs[name] = data
		return d
	}
	platforms := spec.Platforms
	if platforms == nil {
		platforms = []string{"amd64", "arm64"}
	}
	arches := append([]string{}, platforms...)
	if !spec.NoAttestation {
		n := 1
		if spec.AttestationPerPlatform {
			n = len(platforms)
		}
		for i := 0; i < n; i++ {
			arches = append(arches, "")
		}
	}
	annotate := func(from map[string]string) map[string]any {
		out := map[string]any{}
		for k, v := range from {
			out[k] = v
		}
		return out
	}

	var children []map[string]any
	for i, arch := range arches {
		cfg := map[string]any{"arch": arch, "n": filepath.Base(path)}
		if arch == "" {
			cfg["i"] = i
		}
		if arch != "" && len(spec.Labels) > 0 {
			cfg["config"] = map[string]any{"Labels": annotate(spec.Labels)}
		}
		configData := marshal(t, cfg)
		layerData := []byte(fmt.Sprintf("layer %s %s", arch, filepath.Base(path)))
		if arch == "" {
			layerData = []byte(fmt.Sprintf("layer %s %s %d", arch, filepath.Base(path), i))
		}
		layer := map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar", "digest": blob(layerData), "size": len(layerData)}
		manifest := map[string]any{
			"schemaVersion": 2, "mediaType": Manifest,
			"config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": blob(configData), "size": len(configData)},
			"layers": []any{layer},
		}
		child := map[string]any{"mediaType": Manifest}
		if arch != "" {
			child["platform"] = map[string]any{"os": "linux", "architecture": arch}
			if len(spec.Annotations) > 0 {
				manifest["annotations"] = annotate(spec.Annotations)
			}
		} else {
			child["platform"] = map[string]any{"os": "unknown", "architecture": "unknown"}
			child["annotations"] = map[string]any{"vnd.docker.reference.type": "attestation-manifest"}
			predicates := spec.Predicates
			if predicates == nil {
				predicates = []string{"https://slsa.dev/provenance/v0.2", "https://spdx.dev/Document"}
			}
			var layers []any
			for j, p := range predicates {
				data := []byte(fmt.Sprintf("statement %d %d %s", i, j, filepath.Base(path)))
				layers = append(layers, map[string]any{"mediaType": "application/vnd.in-toto+json", "digest": blob(data),
					"size": len(data), "annotations": map[string]any{"in-toto.io/predicate-type": p}})
			}
			manifest["layers"] = layers
		}
		raw := marshal(t, manifest)
		child["digest"], child["size"] = blob(raw), len(raw)
		children = append(children, child)
	}
	index := map[string]any{"schemaVersion": 2, "mediaType": Index, "manifests": children}
	annotations := annotate(spec.Annotations)
	if spec.Revision != "" {
		annotations["org.opencontainers.image.revision"] = spec.Revision
	}
	if len(annotations) > 0 {
		index["annotations"] = annotations
	}
	if spec.MutateIndex != nil {
		spec.MutateIndex(index)
	}
	indexRaw := marshal(t, index)
	top := blob(indexRaw)

	var tops []map[string]any
	for _, ref := range spec.Refs {
		tops = append(tops, map[string]any{"mediaType": Index, "digest": top, "size": len(indexRaw),
			"annotations": map[string]any{"io.containerd.image.name": ref}})
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(name string, data []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0o644, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	add("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`))
	add("index.json", marshal(t, map[string]any{"schemaVersion": 2, "manifests": tops}))
	for _, name := range order {
		add(name, blobs[name])
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return top
}

// RefsFor are the two refs a layout of an image carries: :main and :sha-<short>.
func RefsFor(name, short string) []string {
	return []string{"ghcr.io/open-feature-forking/" + name + ":main", "ghcr.io/open-feature-forking/" + name + ":sha-" + short}
}

type manifestKey struct{ repo, digest string }

// Request is one request the fake registry served.
type Request struct {
	Method, Target, ContentType, Authorization, Accept string
}

// Registry is the part of the distribution API that publish uses, in process. Its state is public: tests read it.
type Registry struct {
	Server *httptest.Server

	mu        sync.Mutex
	blobs     map[[2]string][]byte
	manifests map[manifestKey]stored
	tags      map[[2]string]string
	// Pushed lists (repository, reference) of every manifest PUT, in order.
	pushed [][2]string
	// Requests lists every request, in order.
	requests []Request

	// Token, when not empty, makes the registry answer 401 with a Bearer challenge to every request that does not carry
	// it; the challenge's realm is this server's /token, which wants the Basic login User:Password.
	Token, User, Password string
	// TokenScopes records the scope of every token request.
	tokenScopes []string
	// Location, when set, turns the path of an upload into the Location header the registry answers with.
	Location func(path string) string
	// Realm, when set, is the realm of the Bearer challenge instead of this server's /token.
	Realm string
	// ManifestDigest, when set, replaces the Docker-Content-Digest header of a manifest PUT; actual is the digest of what was stored.
	ManifestDigest func(reference, actual string) string
}

type stored struct {
	media string
	body  []byte
}

// NewRegistry starts a registry; it stops with the test.
func NewRegistry(t testing.TB) *Registry {
	t.Helper()
	r := &Registry{blobs: map[[2]string][]byte{}, manifests: map[manifestKey]stored{}, tags: map[[2]string]string{}}
	r.Server = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.Server.Close)
	return r
}

// Host is the host:port to put in REGISTRY_HOST.
func (r *Registry) Host() string { return strings.TrimPrefix(r.Server.URL, "http://") }

// Tag returns the digest a tag holds, or "".
func (r *Registry) Tag(repo, tag string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tags[[2]string{repo, tag}]
}

// HasManifest reports whether a manifest is stored by digest.
func (r *Registry) HasManifest(repo, digest string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.manifests[manifestKey{repo, digest}]
	return ok
}

// Manifest returns a stored manifest's body.
func (r *Registry) Manifest(repo, digest string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manifests[manifestKey{repo, digest}].body
}

// Snapshot renders everything stored, for a comparison of two registries.
func (r *Registry) Snapshot() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var lines []string
	for k := range r.blobs {
		lines = append(lines, "blob "+k[0]+" "+k[1])
	}
	for k, v := range r.manifests {
		lines = append(lines, "manifest "+k.repo+" "+k.digest+" "+v.media)
	}
	for k, v := range r.tags {
		lines = append(lines, "tag "+k[0]+":"+k[1]+" "+v)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func repoOf(path string) string {
	rest := path[strings.Index(path, "/v2/")+len("/v2/"):]
	rest, _, _ = strings.Cut(rest, "/blobs/")
	rest, _, _ = strings.Cut(rest, "/manifests/")
	return rest
}

func (r *Registry) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	send := func(code int, headers map[string]string, body []byte) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.WriteHeader(code)
		if req.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	}
	if req.URL.Path == "/token" {
		r.tokenScopes = append(r.tokenScopes, req.URL.Query().Get("scope"))
		user, pass, ok := req.BasicAuth()
		if !ok || user != r.User || pass != r.Password {
			send(http.StatusUnauthorized, nil, nil)
			return
		}
		body, _ := json.Marshal(map[string]string{"token": r.Token})
		send(http.StatusOK, nil, body)
		return
	}
	content, _ := readAll(req)
	r.requests = append(r.requests, Request{req.Method, req.URL.RequestURI(), req.Header.Get("Content-Type"),
		req.Header.Get("Authorization"), req.Header.Get("Accept")})
	if r.Token != "" && req.Header.Get("Authorization") != "Bearer "+r.Token {
		realm := r.Server.URL + "/token"
		if r.Realm != "" {
			realm = r.Realm
		}
		send(http.StatusUnauthorized, map[string]string{"WWW-Authenticate": fmt.Sprintf(
			`Bearer realm="%s",service="fake"`, realm)}, nil)
		return
	}
	path := req.URL.Path
	repo := repoOf(path)
	switch req.Method {
	case http.MethodPost:
		location := path + "1"
		if r.Location != nil {
			location = r.Location(location)
		}
		send(http.StatusAccepted, map[string]string{"Location": location}, nil)
	case http.MethodPut:
		if strings.Contains(path, "/blobs/uploads/") {
			want := req.URL.Query().Get("digest")
			if Digest(content) != want {
				send(http.StatusBadRequest, nil, nil)
				return
			}
			r.blobs[[2]string{repo, want}] = content
			send(http.StatusCreated, nil, nil)
			return
		}
		reference := path[strings.LastIndex(path, "/manifests/")+len("/manifests/"):]
		got := Digest(content)
		if strings.HasPrefix(reference, "sha256:") && reference != got {
			send(http.StatusBadRequest, nil, nil)
			return
		}
		r.manifests[manifestKey{repo, got}] = stored{req.Header.Get("Content-Type"), content}
		if !strings.HasPrefix(reference, "sha256:") {
			r.tags[[2]string{repo, reference}] = got
		}
		r.pushed = append(r.pushed, [2]string{repo, reference})
		header := got
		if r.ManifestDigest != nil {
			header = r.ManifestDigest(reference, got)
		}
		send(http.StatusCreated, map[string]string{"Docker-Content-Digest": header}, nil)
	default: // HEAD and GET
		if strings.Contains(path, "/blobs/") {
			if _, ok := r.blobs[[2]string{repo, path[strings.LastIndex(path, "/")+1:]}]; ok {
				send(http.StatusOK, nil, nil)
			} else {
				send(http.StatusNotFound, nil, nil)
			}
			return
		}
		reference := path[strings.LastIndex(path, "/manifests/")+len("/manifests/"):]
		key := reference
		if !strings.HasPrefix(reference, "sha256:") {
			key = r.tags[[2]string{repo, reference}]
		}
		m, ok := r.manifests[manifestKey{repo, key}]
		if !ok {
			send(http.StatusNotFound, nil, nil)
			return
		}
		send(http.StatusOK, map[string]string{"Content-Type": m.media, "Docker-Content-Digest": key}, m.body)
	}
}

func readAll(req *http.Request) ([]byte, error) {
	var b bytes.Buffer
	_, err := b.ReadFrom(req.Body)
	return b.Bytes(), err
}

// Script is the path of a Python helper under .github/scripts, for the parity tests of the packages in tools/ci/internal.
func Script(name string) string {
	abs, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".github", "scripts", name))
	if err != nil {
		panic(err)
	}
	return abs
}

// Python returns the python3 the parity tests run the helpers with. A test that cannot find one is skipped, except in
// GitHub Actions, where a skipped parity test would hide a regression: there it fails.
func Python(t testing.TB) string {
	t.Helper()
	path, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal("python3 is needed for the parity tests and was not found")
		}
		t.Skip("python3 not found")
	}
	return path
}

// RunPython runs a Python helper and returns its exit code, stdout and stderr. dir is the working directory ("" for the
// test's own); env entries are added to the environment.
func RunPython(t testing.TB, dir string, env []string, stdin string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(Python(t), append([]string{"-I"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return code, stdout.String(), stderr.String()
}

// Set changes the registry's behaviour (Token, User, Password, ManifestDigest) while it is idle, under its lock.
func (r *Registry) Set(f func(*Registry)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(r)
}

// Requests returns a copy of every request served, in order.
func (r *Registry) Requests() []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Request(nil), r.requests...)
}

// Pushed returns a copy of (repository, reference) of every manifest PUT, in order.
func (r *Registry) Pushed() [][2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]string(nil), r.pushed...)
}

// BlobCount is the number of blobs stored.
func (r *Registry) BlobCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.blobs)
}

// TagNames returns every (repository, tag) stored.
func (r *Registry) TagNames() [][2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][2]string, 0, len(r.tags))
	for k := range r.tags {
		out = append(out, k)
	}
	return out
}

// TokenScopes returns the scope of every token request, in order.
func (r *Registry) TokenScopes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.tokenScopes...)
}
