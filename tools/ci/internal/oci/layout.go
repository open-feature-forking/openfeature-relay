// Package oci reads the OCI layouts that `docker buildx build --output type=oci` writes: the checks of the image builds
// (step 17), the marking they carry, and the docker-archive `docker load` needs. It needs only the standard library.
package oci

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// The labels and annotations of the proof-of-concept marking (Q4).
const (
	Title       = "org.opencontainers.image.title"
	Description = "org.opencontainers.image.description"
	Source      = "org.opencontainers.image.source"
	Revision    = "org.opencontainers.image.revision"
	Version     = "org.opencontainers.image.version"
	Created     = "org.opencontainers.image.created"
	Licenses    = "org.opencontainers.image.licenses"

	// SourceURL is the repository the images are built from.
	SourceURL = "https://github.com/open-feature-forking/openfeature-relay"
	// DescriptionText is the description every published image carries.
	DescriptionText = "Proof of concept from the OpenFeature Relay incubator, built from main. " +
		"Not an official OpenFeature image; not for production."

	// ImageNameAnnotation names the ref an entry of a layout's index.json stands for.
	ImageNameAnnotation = "io.containerd.image.name"
)

// Platform is the platform of a manifest descriptor.
type Platform struct {
	OS           *string `json:"os"`
	Architecture string  `json:"architecture"`
}

// Descriptor is an OCI content descriptor.
type Descriptor struct {
	MediaType   string         `json:"mediaType"`
	Digest      string         `json:"digest"`
	Size        int64          `json:"size"`
	Platform    *Platform      `json:"platform"`
	Annotations map[string]any `json:"annotations"`
}

// Index is an image index (or the layout's index.json).
type Index struct {
	Manifests   []Descriptor   `json:"manifests"`
	Annotations map[string]any `json:"annotations"`
}

// Manifest is an image manifest.
type Manifest struct {
	Config      Descriptor     `json:"config"`
	Layers      []Descriptor   `json:"layers"`
	Annotations map[string]any `json:"annotations"`
}

// ImageConfig is the part of an image configuration the checks read.
type ImageConfig struct {
	Config struct {
		Labels map[string]any `json:"Labels"`
	} `json:"config"`
}

type entry struct {
	off, size int64
	header    tar.Header
}

// Layout is an OCI layout in a tar file. Blobs are read from the file on demand, so a layout of several hundred
// megabytes is never held in memory.
type Layout struct {
	file    *os.File
	entries map[string]entry
}

// counter counts the bytes the tar reader consumed, so the offset of each member's data is known.
type counter struct {
	f *os.File
	n int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.f.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *counter) Seek(offset int64, whence int) (int64, error) {
	n, err := c.f.Seek(offset, whence)
	if err == nil {
		c.n = n
	}
	return n, err
}

// OpenLayout opens the tar at path and indexes its members. The last member of a name wins, as in Python's tarfile.
func OpenLayout(path string) (*Layout, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	l := &Layout{file: f, entries: map[string]entry{}}
	c := &counter{f: f}
	tr := tar.NewReader(c)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		l.entries[h.Name] = entry{off: c.n, size: h.Size, header: *h}
	}
	return l, nil
}

// Close closes the layout's file.
func (l *Layout) Close() error { return l.file.Close() }

// Open returns a reader of the named member. Every call returns a fresh reader at the start of the member.
func (l *Layout) Open(name string) (*io.SectionReader, error) {
	e, ok := l.entries[name]
	if !ok {
		return nil, fmt.Errorf("%s: not in the layout", name)
	}
	return io.NewSectionReader(l.file, e.off, e.size), nil
}

// Raw returns the bytes of the named member.
func (l *Layout) Raw(name string) ([]byte, error) {
	r, err := l.Open(name)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// BlobName is the member name of the blob with the given digest.
func BlobName(digest string) (string, error) {
	if !ValidDigest(digest) {
		return "", fmt.Errorf("malformed digest %q", digest)
	}
	return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:"), nil
}

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ValidDigest reports whether s is a sha256 digest. Digests come from a layout and go into registry URLs, so nothing else
// is accepted (the Python took anything with a colon).
func ValidDigest(s string) bool { return digestPattern.MatchString(s) }

// RawBlob returns the bytes of the blob with the given digest.
func (l *Layout) RawBlob(digest string) ([]byte, error) {
	name, err := BlobName(digest)
	if err != nil {
		return nil, err
	}
	return l.Raw(name)
}

// Blob decodes the JSON blob with the given digest into v.
func (l *Layout) Blob(digest string, v any) error {
	raw, err := l.RawBlob(digest)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", digest, err)
	}
	return nil
}

// Refs maps every ref the layout carries to its descriptor in the top-level index.
func (l *Layout) Refs() (map[string]Descriptor, error) {
	raw, err := l.Raw("index.json")
	if err != nil {
		return nil, err
	}
	var top Index
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("index.json: %w", err)
	}
	found := map[string]Descriptor{}
	for _, m := range top.Manifests {
		name, _ := m.Annotations[ImageNameAnnotation].(string)
		found[name] = m
	}
	return found, nil
}

// Index reads the image index of a ref.
func (l *Layout) Index(ref string) (Index, error) {
	var idx Index
	refs, err := l.Refs()
	if err != nil {
		return idx, err
	}
	d, ok := refs[ref]
	if !ok {
		return idx, fmt.Errorf("the layout has no ref %q", ref)
	}
	return idx, l.Blob(d.Digest, &idx)
}

// PlatformManifests returns the manifests that are images (an attestation manifest has the platform unknown).
func PlatformManifests(idx Index) []Descriptor {
	var out []Descriptor
	for _, m := range idx.Manifests {
		if m.Platform != nil && m.Platform.OS != nil && *m.Platform.OS != "unknown" {
			out = append(out, m)
		}
	}
	return out
}

// Attestations returns the attestation manifests of an index.
func Attestations(idx Index) []Descriptor {
	var out []Descriptor
	for _, m := range idx.Manifests {
		if t, _ := m.Annotations["vnd.docker.reference.type"].(string); t == "attestation-manifest" {
			out = append(out, m)
		}
	}
	return out
}

// Marking returns the labels and annotations every published image carries (Q4), in the order they are printed.
func Marking(name, revision, version string) Pairs {
	return Pairs{
		{Title, name}, {Description, DescriptionText}, {Source, SourceURL}, {Revision, revision},
		{Version, version}, {Licenses, "Apache-2.0"},
	}
}
