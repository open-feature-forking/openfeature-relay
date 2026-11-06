// Package publish pushes the OCI layouts the image builds write to ghcr.io/open-feature-forking/ (step 17), and moves the
// main tags. It runs in the jobs that hold `packages: write`, so it holds the destinations itself and refuses any other:
// the seven names below under ghcr.io/open-feature-forking/ and nothing else. A layout names two refs under
// ghcr.io/open-feature/..., which are never read as destinations. Every layout is checked against the allowlist before
// the first byte is pushed. The registry host is ghcr.io; REGISTRY_HOST may name a local registry (localhost or 127.0.0.1,
// plain http) for the rehearsal. Credentials: REGISTRY_USER and REGISTRY_TOKEN, as the user and token of a Basic login.
//
// There is one image per name: no FIPS variant is published. The package needs only the standard library.
package publish

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/oci"
)

const (
	// Namespace is the only organization images are pushed to.
	Namespace = "open-feature-forking"
	// Testbed is the one image whose layout may be missing from a run.
	Testbed = "flagd-testbed"
)

// Names is every image that is published, in the order they are pushed.
var Names = []string{"flagd-full", "flagd-minimal", "flagd-minimal-otel", "flagd-cloud", "flagd-kubernetes", "flagd-sync", Testbed}

var tagPattern = regexp.MustCompile(`^(main|sha-[0-9a-f]{7})$`)

// Refused is returned when a layout or an environment asks for a destination that is not allowed. The command exits 3.
type Refused struct{ Message string }

func (r *Refused) Error() string { return r.Message }

func refuse(format string, args ...any) error { return &Refused{fmt.Sprintf(format, args...)} }

// Registry is what push and move-main need of a registry.
type Registry interface {
	PushBlob(repository, digest string, stream func() io.Reader, size int64) error
	PushManifest(repository, reference, mediaType string, content []byte) (string, error)
	GetManifest(repository, reference string) (*Manifest, error)
}

// RegistryFromEnv builds the registry client from REGISTRY_HOST, REGISTRY_USER and REGISTRY_TOKEN.
func RegistryFromEnv() (Registry, error) {
	host := os.Getenv("REGISTRY_HOST")
	if host == "" {
		host = "ghcr.io"
	}
	if host != "ghcr.io" && !localHost(host) {
		return nil, refuse("REGISTRY_HOST %s is neither ghcr.io nor a local registry", host)
	}
	return NewClient(host, os.Getenv("REGISTRY_USER"), os.Getenv("REGISTRY_TOKEN")), nil
}

func resolveNames(names []string) ([]string, error) {
	if len(names) == 0 {
		return slices.Clone(Names), nil
	}
	for _, n := range names {
		if !slices.Contains(Names, n) {
			return nil, refuse("%s is not one of the images to publish", n)
		}
	}
	return names, nil
}

// layoutFiles returns name -> path of the layouts to push, and the optional ones that are missing.
func layoutFiles(directory string, names []string) (map[string]string, []string, error) {
	found := map[string]string{}
	var missing []string
	for _, name := range names {
		path := filepath.Join(directory, name+".tar")
		if _, err := os.Stat(path); err == nil {
			found[name] = path
		} else if name == Testbed {
			missing = append(missing, name)
		} else {
			return nil, nil, refuse("the layout of %s is missing: %s", name, path)
		}
	}
	return found, missing, nil
}

// checkDestinations requires every ref a layout carries to be the image's own repository under the allowlist, with an
// allowed tag.
func checkDestinations(key string, layout *oci.Layout, short string) error {
	if !slices.Contains(Names, key) {
		return refuse("%s is not one of the images to publish", key)
	}
	want := []string{"ghcr.io/" + Namespace + "/" + key + ":main", "ghcr.io/" + Namespace + "/" + key + ":sha-" + short}
	refs, err := layout.Refs()
	if err != nil {
		return err
	}
	have := make([]string, 0, len(refs))
	for ref := range refs {
		have = append(have, ref)
	}
	sort.Strings(have)
	for _, ref := range have {
		repository, tag := "", ref
		if i := strings.LastIndex(ref, ":"); i >= 0 {
			repository, tag = ref[:i], ref[i+1:]
		}
		if repository != "ghcr.io/"+Namespace+"/"+key || !tagPattern.MatchString(tag) {
			return refuse("%s: the layout names %s, which is not a destination of %s", key, ref, key)
		}
	}
	sort.Strings(want)
	if !slices.Equal(have, want) {
		return refuse("%s: the layout carries %s, not %s", key, oci.Repr(have), oci.Repr(want))
	}
	return nil
}

// PushOptions are the arguments of `publish push`.
type PushOptions struct {
	Layouts  string   // directory of <name>.tar layouts
	Short    string   // seven hex digits of the commit
	Names    []string // default: every image
	Outputs  string   // file to append name=digest lines to
	Registry Registry // default: from the environment
}

// checkDigests requires every digest a layout will put into a registry URL to be a sha256 digest, before the first byte is
// pushed. The Python took anything with a colon (stricter here).
func checkDigests(key string, layout *oci.Layout, short string) error {
	refs, err := layout.Refs()
	if err != nil {
		return err
	}
	descriptor := refs["ghcr.io/"+Namespace+"/"+key+":sha-"+short]
	bad := func(what, digest string) error {
		return refuse("%s: the %s digest %q is not a sha256 digest", key, what, digest)
	}
	if !oci.ValidDigest(descriptor.Digest) {
		return bad("index", descriptor.Digest)
	}
	var index oci.Index
	if err := layout.Blob(descriptor.Digest, &index); err != nil {
		return err
	}
	for _, child := range index.Manifests {
		if !oci.ValidDigest(child.Digest) {
			return bad("manifest", child.Digest)
		}
		var manifest oci.Manifest
		if err := layout.Blob(child.Digest, &manifest); err != nil {
			return err
		}
		if !oci.ValidDigest(manifest.Config.Digest) {
			return bad("config", manifest.Config.Digest)
		}
		for _, l := range manifest.Layers {
			if !oci.ValidDigest(l.Digest) {
				return bad("layer", l.Digest)
			}
		}
	}
	return nil
}

func pushLayout(reg Registry, key string, layout *oci.Layout, short string, log func(string)) (string, error) {
	repository := Namespace + "/" + key
	ref := "ghcr.io/" + repository + ":sha-" + short
	refs, err := layout.Refs()
	if err != nil {
		return "", err
	}
	descriptor, ok := refs[ref]
	if !ok {
		return "", fmt.Errorf("%s: the layout has no ref %s", repository, ref)
	}
	indexDigest := descriptor.Digest
	var index oci.Index
	if err := layout.Blob(indexDigest, &index); err != nil {
		return "", err
	}

	blob := func(digest string, size int64) error {
		name, err := oci.BlobName(digest)
		if err != nil {
			return err
		}
		if _, err := layout.Open(name); err != nil {
			return err
		}
		return reg.PushBlob(repository, digest, func() io.Reader {
			r, _ := layout.Open(name)
			return r
		}, size)
	}
	for _, child := range index.Manifests {
		var manifest oci.Manifest
		if err := layout.Blob(child.Digest, &manifest); err != nil {
			return "", err
		}
		if err := blob(manifest.Config.Digest, manifest.Config.Size); err != nil {
			return "", err
		}
		for _, item := range manifest.Layers {
			if err := blob(item.Digest, item.Size); err != nil {
				return "", err
			}
		}
	}
	// the platform and attestation manifests by digest, then the index by digest, then its tags
	for _, child := range index.Manifests {
		raw, err := layout.RawBlob(child.Digest)
		if err != nil {
			return "", err
		}
		got, err := reg.PushManifest(repository, child.Digest, child.MediaType, raw)
		if err != nil {
			return "", err
		}
		if got != child.Digest {
			return "", fmt.Errorf("%s: pushed %s, the registry stored %s", repository, child.Digest, got)
		}
	}
	raw, err := layout.RawBlob(indexDigest)
	if err != nil {
		return "", err
	}
	got, err := reg.PushManifest(repository, indexDigest, descriptor.MediaType, raw)
	if err != nil {
		return "", err
	}
	if got != indexDigest {
		return "", fmt.Errorf("%s: pushed %s, the registry stored %s", repository, indexDigest, got)
	}
	got, err = reg.PushManifest(repository, "sha-"+short, descriptor.MediaType, raw)
	if err != nil {
		return "", err
	}
	if got != indexDigest {
		return "", fmt.Errorf("%s: the tag sha-%s holds %s, not %s", repository, short, got, indexDigest)
	}
	log(fmt.Sprintf("- pushed %s:sha-%s = %s", repository, short, indexDigest))
	return indexDigest, nil
}

// Push pushes every layout by digest and under its sha-<short> tag. It never pushes a main tag: MoveMain does.
func Push(o PushOptions, log func(string)) (err error) {
	names, err := resolveNames(o.Names)
	if err != nil {
		return err
	}
	files, missing, err := layoutFiles(o.Layouts, names)
	if err != nil {
		return err
	}
	layouts := map[string]*oci.Layout{}
	defer func() {
		for _, l := range layouts {
			_ = l.Close()
		}
	}()
	for _, name := range names {
		path, ok := files[name]
		if !ok {
			continue
		}
		l, err := oci.OpenLayout(path)
		if err != nil {
			return err
		}
		layouts[name] = l
	}
	for _, name := range names {
		if l, ok := layouts[name]; ok {
			if err := checkDestinations(name, l, o.Short); err != nil {
				return err
			}
			if err := checkDigests(name, l, o.Short); err != nil {
				return err
			}
		}
	}
	reg := o.Registry
	if reg == nil {
		if reg, err = RegistryFromEnv(); err != nil {
			return err
		}
	}
	for _, name := range missing {
		log(fmt.Sprintf("- skipped %s: no layout was built in this run", name))
	}
	type output struct{ key, digest string }
	var outputs []output
	for _, name := range names {
		l, ok := layouts[name]
		if !ok {
			continue
		}
		digest, err := pushLayout(reg, name, l, o.Short, log)
		if err != nil {
			return err
		}
		outputs = append(outputs, output{strings.ReplaceAll(name, "-", "_"), digest})
	}
	if o.Outputs != "" {
		f, err := os.OpenFile(o.Outputs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
		if err != nil {
			return err
		}
		var b strings.Builder
		for _, out := range outputs {
			b.WriteString(out.key + "=" + out.digest + "\n")
		}
		_, werr := f.WriteString(b.String())
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return werr
	}
	return nil
}

// MoveOptions are the arguments of `publish move-main`.
type MoveOptions struct {
	Short    string
	Sha      string   // the full commit the sha-<short> images were built from
	Names    []string // default: every image
	Force    bool
	GitDir   string // the checkout git runs in; default: the working directory
	Registry Registry

	gitRun func(args ...string) bool // replaces git, for the tests
}

func (o MoveOptions) git(args ...string) bool {
	if o.gitRun != nil {
		return o.gitRun(args...)
	}
	if o.GitDir != "" {
		args = append([]string{"-C", o.GitDir}, args...)
	}
	cmd := exec.Command("git", args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run() == nil
}

// A revision comes from the registry: one that looks like an option never reaches git.
func (o MoveOptions) isAncestor(old string) bool {
	return !strings.HasPrefix(old, "-") && o.git("merge-base", "--is-ancestor", old, o.Sha)
}

func (o MoveOptions) hasCommit(sha string) bool {
	return !strings.HasPrefix(sha, "-") && o.git("cat-file", "-e", sha+"^{commit}")
}

// revisionOf reads the revision annotation of an image index; an index without one has "".
func revisionOf(index []byte) (string, error) {
	var doc struct {
		Annotations map[string]any `json:"annotations"`
	}
	if err := json.Unmarshal(index, &doc); err != nil {
		return "", err
	}
	revision, _ := doc.Annotations[oci.Revision].(string)
	return revision, nil
}

func short7(s string) string {
	if r := []rune(s); len(r) > 7 {
		return string(r[:7])
	}
	return s
}

// MoveMain moves the main tags to the sha-<short> images, only if the commit :main points at is an ancestor of Sha. A
// commit this checkout does not have (main was force-pushed) is no ancestor, so :main would stay where it is for good:
// Force moves it anyway, but only for a commit this checkout lacks: a commit it has that is no ancestor keeps :main.
func MoveMain(o MoveOptions, log func(string)) error {
	names := o.Names
	if len(names) == 0 {
		names = Names
	}
	reg := o.Registry
	if reg == nil {
		var err error
		if reg, err = RegistryFromEnv(); err != nil {
			return err
		}
	}
	for _, name := range names {
		if !slices.Contains(Names, name) {
			return refuse("%s is not one of the images to publish", name)
		}
		repository := Namespace + "/" + name
		next, err := reg.GetManifest(repository, "sha-"+o.Short)
		if err != nil {
			return err
		}
		if next == nil {
			log(fmt.Sprintf("- kept %s:main: sha-%s was not published", repository, o.Short))
			continue
		}
		built, err := revisionOf(next.Body)
		if err != nil {
			return fmt.Errorf("%s:sha-%s: %w", repository, o.Short, err)
		}
		if built != o.Sha {
			// The Python did not look: the image under sha-<short> must have been built from the commit --sha names.
			return fmt.Errorf("%s:sha-%s was built from %q, not from %s", repository, o.Short, built, o.Sha)
		}
		current, err := reg.GetManifest(repository, "main")
		if err != nil {
			return err
		}
		if current != nil {
			revision, err := revisionOf(current.Body)
			if err != nil {
				return fmt.Errorf("%s:main: %w", repository, err)
			}
			if revision == o.Sha {
				log(fmt.Sprintf("- %s:main already holds %s", repository, short7(o.Sha)))
				continue
			}
			lacks := revision != "" && !o.hasCommit(revision)
			// Force only covers a revision this checkout lacks (main was force-pushed); a commit it has that is no
			// ancestor of Sha (an older or a sibling commit) keeps :main either way.
			if (!o.Force || !lacks) && (revision == "" || !o.isAncestor(revision)) {
				hint := ""
				if lacks {
					hint = " (this checkout lacks it: was main force-pushed? run the build manually with move-main-anyway)"
				}
				held := short7(revision)
				if held == "" {
					held = "an unknown commit"
				}
				log(fmt.Sprintf("- kept %s:main: it holds %s, which is not an ancestor of %s%s", repository, held, short7(o.Sha), hint))
				continue
			}
		}
		got, err := reg.PushManifest(repository, "main", next.MediaType, next.Body)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(next.Body)
		want := "sha256:" + hex.EncodeToString(sum[:])
		if got != want {
			return fmt.Errorf("%s: the tag main holds %s, not %s", repository, got, want)
		}
		log(fmt.Sprintf("- moved %s:main to %s = %s", repository, short7(o.Sha), want))
	}
	return nil
}
