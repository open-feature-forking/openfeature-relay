package publish

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/ocitest"
)

// These tests are the rows of .github/scripts/test_publish_images.py, one per test method, plus the rows the port adds
// for the Go client (marked "port:"). The registry is in process; nothing here talks to ghcr.io.

const (
	short = "abc1234"
)

func fullSha(c string) string { return strings.Repeat(c, 40) }

type env struct {
	t   *testing.T
	dir string
	reg *ocitest.Registry
	log []string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	reg := ocitest.NewRegistry(t)
	t.Setenv("REGISTRY_HOST", reg.Host())
	t.Setenv("REGISTRY_USER", "")
	t.Setenv("REGISTRY_TOKEN", "")
	return &env{t: t, dir: t.TempDir(), reg: reg}
}

func (e *env) write(name string, refs []string, revision string) string {
	return ocitest.WriteLayout(e.t, filepath.Join(e.dir, name+".tar"), ocitest.LayoutSpec{Refs: refs, Revision: revision})
}

func (e *env) layouts(short, revision string, names ...string) map[string]string {
	if len(names) == 0 {
		names = Names
	}
	out := map[string]string{}
	for _, n := range names {
		out[n] = e.write(n, ocitest.RefsFor(n, short), revision)
	}
	return out
}

func (e *env) push(opts ...func(*PushOptions)) error {
	o := PushOptions{Layouts: e.dir, Short: short}
	for _, f := range opts {
		f(&o)
	}
	return Push(o, func(s string) { e.log = append(e.log, s) })
}

func (e *env) pushed() int {
	n := 0
	for _, m := range e.log {
		if strings.HasPrefix(m, "- pushed ") {
			n++
		}
	}
	return n
}

func wantRefused(t *testing.T, err error) {
	t.Helper()
	var r *Refused
	if !errors.As(err, &r) {
		t.Fatalf("error = %v, want a refusal", err)
	}
}

func TestPushesByDigestAndUnderTheShaTagAndNeverUnderMain(t *testing.T) {
	e := newEnv(t)
	digests := e.layouts(short, fullSha("a"))
	if err := e.push(); err != nil {
		t.Fatal(err)
	}
	for name, want := range digests {
		repo := "open-feature-forking/" + name
		if got := e.reg.Tag(repo, "sha-"+short); got != want {
			t.Errorf("%s: sha tag = %s, want %s", name, got, want)
		}
		// the index, both platform manifests and the attestation manifest are stored by digest
		var index struct {
			Manifests []struct{ Digest string } `json:"manifests"`
		}
		if err := json.Unmarshal(e.reg.Manifest(repo, want), &index); err != nil {
			t.Fatal(err)
		}
		if len(index.Manifests) != 3 {
			t.Errorf("%s: %d children, want 3", name, len(index.Manifests))
		}
		for _, c := range index.Manifests {
			if !e.reg.HasManifest(repo, c.Digest) {
				t.Errorf("%s: child %s not stored", name, c.Digest)
			}
		}
	}
	for _, k := range e.reg.TagNames() {
		if strings.HasPrefix(k[1], "main") {
			t.Errorf("a main tag was pushed: %v", k)
		}
	}
	if e.pushed() != 7 {
		t.Errorf("%d pushed lines, want 7: %v", e.pushed(), e.log)
	}
}

func TestOutputsCarryEachPushedDigest(t *testing.T) {
	e := newEnv(t)
	digests := e.layouts(short, fullSha("a"))
	out := filepath.Join(e.dir, "outputs")
	if err := os.WriteFile(out, []byte("earlier=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.push(func(o *PushOptions) { o.Outputs = out }); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	lines := strings.Fields(string(raw))
	if lines[0] != "earlier=1" {
		t.Errorf("the outputs file was not appended to: %v", lines)
	}
	got := map[string]string{}
	for _, l := range lines[1:] {
		k, v, _ := strings.Cut(l, "=")
		got[k] = v
	}
	if len(got) != len(digests) {
		t.Fatalf("got %d outputs, want %d", len(got), len(digests))
	}
	for name, d := range digests {
		if got[strings.ReplaceAll(name, "-", "_")] != d {
			t.Errorf("%s: output %q, want %s", name, got[strings.ReplaceAll(name, "-", "_")], d)
		}
	}
}

// The refused destinations: each row replaces flagd-sync's layout and must be refused before anything is pushed.
func TestRefusedBeforeAnythingIsPushed(t *testing.T) {
	tests := []struct {
		name string
		refs []string
	}{
		{"a destination outside the seven", []string{"ghcr.io/open-feature/flagd-sync:latest", "ghcr.io/open-feature/flagd:v1.2.3"}},
		{"a layout cannot publish into another image's repository", ocitest.RefsFor("flagd-cloud", short)},
		{"a tag other than main or sha", []string{"ghcr.io/open-feature-forking/flagd-sync:latest", "ghcr.io/open-feature-forking/flagd-sync:sha-" + short}},
		{"no FIPS tag is a destination", []string{"ghcr.io/open-feature-forking/flagd-sync:main-fips", "ghcr.io/open-feature-forking/flagd-sync:sha-" + short + "-fips"}},
		{"port: a sha tag of the wrong length", []string{"ghcr.io/open-feature-forking/flagd-sync:main", "ghcr.io/open-feature-forking/flagd-sync:sha-abc12345"}},
		{"port: a layout that carries a third ref", append(ocitest.RefsFor("flagd-sync", short), "ghcr.io/open-feature-forking/flagd-sync:sha-0000000")},
		{"port: a layout that lacks the main ref", ocitest.RefsFor("flagd-sync", short)[1:]},
		{"port: another registry's host", []string{"evil.example.com/open-feature-forking/flagd-sync:main", "evil.example.com/open-feature-forking/flagd-sync:sha-" + short}},
		{"port: the sha of another commit", ocitest.RefsFor("flagd-sync", "1111111")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.layouts(short, fullSha("a"))
			e.write("flagd-sync", tt.refs, fullSha("a"))
			wantRefused(t, e.push())
			if len(e.reg.Pushed()) != 0 || e.reg.BlobCount() != 0 || len(e.reg.Requests()) != 0 {
				t.Errorf("the registry saw %d requests", len(e.reg.Requests()))
			}
		})
	}
}

func TestAMissingTestbedLayoutIsSkippedAndReported(t *testing.T) {
	e := newEnv(t)
	e.layouts(short, fullSha("a"))
	if err := os.Remove(filepath.Join(e.dir, "flagd-testbed.tar")); err != nil {
		t.Fatal(err)
	}
	if err := e.push(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range e.log {
		found = found || m == "- skipped flagd-testbed: no layout was built in this run"
	}
	if !found {
		t.Errorf("no skip line in %v", e.log)
	}
	if e.pushed() != 6 {
		t.Errorf("%d pushed lines, want 6", e.pushed())
	}
}

func TestAMissingDistributionLayoutIsAnError(t *testing.T) {
	e := newEnv(t)
	e.layouts(short, fullSha("a"))
	if err := os.Remove(filepath.Join(e.dir, "flagd-sync.tar")); err != nil {
		t.Fatal(err)
	}
	wantRefused(t, e.push())
	if len(e.reg.Pushed()) != 0 {
		t.Error("something was pushed")
	}
}

func TestOnlyGhcrOrALocalRegistryIsAHost(t *testing.T) {
	tests := []struct {
		host    string
		refused bool
	}{
		{"registry.example.com", true},
		{"ghcr.io:443", true},
		{"port: localhost:80@evil.example.com", true},
		{"port: 127.0.0.1@evil.example.com", true},
		{"port: localhost.evil.example.com", true},
		{"port: localhost:abc", true},
		{"port: localhost:0", true},
		{"port: localhost:99999", true},
		{"port: localhost:5000/path", true},
		{"localhost:5000", false},
		{"127.0.0.1:5000", false},
		{"localhost", false},
		{"ghcr.io", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Setenv("REGISTRY_HOST", strings.TrimPrefix(tt.host, "port: "))
			_, err := RegistryFromEnv()
			if tt.refused {
				wantRefused(t, err)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("a refused host stops a push before any request", func(t *testing.T) {
		e := newEnv(t)
		e.layouts(short, fullSha("a"))
		t.Setenv("REGISTRY_HOST", "registry.example.com")
		wantRefused(t, e.push())
		if len(e.reg.Requests()) != 0 {
			t.Error("the registry saw a request")
		}
	})
}

// git builds the history of a throwaway repository and returns the commit ids it makes.
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "-q")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.dir, "-c", "user.email=a@b", "-c", "user.name=x", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repo) commit(msg string) string {
	r.git("commit", "-q", "--allow-empty", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func (e *env) publishCommit(sha, revision string) {
	e.t.Helper()
	dir := filepath.Join(e.dir, "layouts-"+sha[:7])
	if err := os.Mkdir(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	ocitest.WriteLayout(e.t, filepath.Join(dir, "flagd-sync.tar"), ocitest.LayoutSpec{Refs: ocitest.RefsFor("flagd-sync", sha[:7]), Revision: revision})
	if err := Push(PushOptions{Layouts: dir, Short: sha[:7], Names: []string{"flagd-sync"}}, func(string) {}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) move(r *repo, sha string, force bool) error {
	return MoveMain(MoveOptions{Short: sha[:7], Sha: sha, Names: []string{"flagd-sync"}, Force: force, GitDir: r.dir},
		func(s string) { e.log = append(e.log, s) })
}

const syncRepo = "open-feature-forking/flagd-sync"

func (e *env) kept() bool {
	for _, m := range e.log {
		if strings.HasPrefix(m, "- kept "+syncRepo+":main") {
			return true
		}
	}
	return false
}

func TestMainMovesForwardAndAnOlderCommitPublishedLaterLeavesItAlone(t *testing.T) {
	e := newEnv(t)
	r := newRepo(t)
	old := r.commit("old")
	next := r.commit("new")
	e.publishCommit(next, next)
	e.publishCommit(old, old)

	if err := e.move(r, old, false); err != nil { // no :main yet
		t.Fatal(err)
	}
	if got, want := e.reg.Tag(syncRepo, "main"), e.reg.Tag(syncRepo, "sha-"+old[:7]); got != want || got == "" {
		t.Errorf("main = %s, want the old image %s", got, want)
	}
	if err := e.move(r, next, false); err != nil { // a descendant
		t.Fatal(err)
	}
	if got, want := e.reg.Tag(syncRepo, "main"), e.reg.Tag(syncRepo, "sha-"+next[:7]); got != want {
		t.Errorf("main = %s, want the new image %s", got, want)
	}
	e.log = nil
	if err := e.move(r, old, false); err != nil { // an older commit published after a newer one
		t.Fatal(err)
	}
	if got, want := e.reg.Tag(syncRepo, "main"), e.reg.Tag(syncRepo, "sha-"+next[:7]); got != want {
		t.Errorf("main moved back to %s", got)
	}
	if !e.kept() {
		t.Errorf("no kept line in %v", e.log)
	}
}

// history publishes two commits and moves :main to the old one, whose image carries revisionOfOld ("" means the old
// commit itself, "side" a commit this checkout has that is no ancestor of the new one).
func (e *env) history(revisionOfOld string) (*repo, string) {
	e.t.Helper()
	r := newRepo(e.t)
	old := r.commit("old")
	next := r.commit("new")
	switch revisionOfOld {
	case "":
		revisionOfOld = old
	case "side":
		revisionOfOld = r.git("commit-tree", r.git("rev-parse", "HEAD^{tree}"), "-p", old, "-m", "side")
	}
	e.publishCommit(old, revisionOfOld)
	e.publishCommit(next, next)
	if err := e.move(r, old, false); err != nil {
		e.t.Fatal(err)
	}
	e.log = nil
	return r, next
}

func TestAMainThatHoldsACommitThisCheckoutLacksStaysUntilForced(t *testing.T) {
	e := newEnv(t)
	r, next := e.history(fullSha("f"))
	before := e.reg.Tag(syncRepo, "main")
	if err := e.move(r, next, false); err != nil {
		t.Fatal(err)
	}
	if got := e.reg.Tag(syncRepo, "main"); got != before {
		t.Error("main moved without --force")
	}
	hinted := false
	for _, m := range e.log {
		hinted = hinted || strings.Contains(m, "force-pushed")
	}
	if !hinted {
		t.Errorf("no force-push hint in %v", e.log)
	}
	if err := e.move(r, next, true); err != nil {
		t.Fatal(err)
	}
	if got, want := e.reg.Tag(syncRepo, "main"), e.reg.Tag(syncRepo, "sha-"+next[:7]); got != want {
		t.Errorf("main = %s after --force, want %s", got, want)
	}
}

func TestForceDoesNotMoveMainBackToACommitTheCheckoutHas(t *testing.T) {
	e := newEnv(t)
	r, next := e.history("side")
	before := e.reg.Tag(syncRepo, "main")
	for _, force := range []bool{false, true} {
		e.log = nil
		if err := e.move(r, next, force); err != nil {
			t.Fatal(err)
		}
		if got := e.reg.Tag(syncRepo, "main"); got != before {
			t.Errorf("force=%v: main moved to %s", force, got)
		}
		if !e.kept() {
			t.Errorf("force=%v: no kept line in %v", force, e.log)
		}
	}
}

func TestAMainTagThatDoesNotHoldThePushedDigestIsAnError(t *testing.T) {
	e := newEnv(t)
	r, next := e.history("")
	e.reg.Set(func(r *ocitest.Registry) {
		r.ManifestDigest = func(string) string { return "sha256:" + strings.Repeat("0", 64) }
	})
	client, err := RegistryFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	err = MoveMain(MoveOptions{Short: next[:7], Sha: next, Names: []string{"flagd-sync"}, GitDir: r.dir, Registry: client}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "the tag main holds") {
		t.Fatalf("error = %v", err)
	}
}

// Rows the port adds for move-main.
func TestMoveMainEdges(t *testing.T) {
	t.Run("port: an image that was not published keeps main", func(t *testing.T) {
		e := newEnv(t)
		r, _ := e.history("")
		other := r.commit("third")
		if err := e.move(r, other, false); err != nil {
			t.Fatal(err)
		}
		if len(e.log) != 1 || !strings.Contains(e.log[0], "was not published") {
			t.Errorf("log = %v", e.log)
		}
	})
	t.Run("port: main that already holds the commit is left alone", func(t *testing.T) {
		e := newEnv(t)
		r, next := e.history("")
		if err := e.move(r, next, false); err != nil {
			t.Fatal(err)
		}
		e.log = nil
		before := len(e.reg.Pushed())
		if err := e.move(r, next, false); err != nil {
			t.Fatal(err)
		}
		if len(e.log) != 1 || !strings.Contains(e.log[0], "already holds") || len(e.reg.Pushed()) != before {
			t.Errorf("log = %v, pushed %d more", e.log, len(e.reg.Pushed())-before)
		}
	})
	t.Run("port: a main without a revision annotation keeps main, with or without --force", func(t *testing.T) {
		e := newEnv(t)
		r := newRepo(t)
		old := r.commit("old")
		next := r.commit("new")
		e.publishCommit(old, "")
		e.publishCommit(next, next)
		if err := e.move(r, old, false); err != nil {
			t.Fatal(err)
		}
		before := e.reg.Tag(syncRepo, "main")
		for _, force := range []bool{false, true} {
			e.log = nil
			if err := e.move(r, next, force); err != nil {
				t.Fatal(err)
			}
			if e.reg.Tag(syncRepo, "main") != before || !strings.Contains(strings.Join(e.log, "\n"), "an unknown commit") {
				t.Errorf("force=%v: log = %v", force, e.log)
			}
		}
	})
	t.Run("port: a revision that looks like an option is treated as a commit this checkout lacks", func(t *testing.T) {
		e := newEnv(t)
		r, next := e.history("--upload-pack=x")
		if err := e.move(r, next, false); err != nil {
			t.Fatal(err)
		}
		if !e.kept() {
			t.Errorf("log = %v", e.log)
		}
	})
	t.Run("port: a name outside the seven is refused", func(t *testing.T) {
		err := MoveMain(MoveOptions{Short: short, Sha: fullSha("a"), Names: []string{"flagd"}}, func(string) {})
		wantRefused(t, err)
	})
}

// Rows the port adds for the registry client, which the Python covered only through the rehearsal.
func TestRegistryClient(t *testing.T) {
	t.Run("port: Basic credentials go to a local registry", func(t *testing.T) {
		e := newEnv(t)
		t.Setenv("REGISTRY_USER", "bot")
		t.Setenv("REGISTRY_TOKEN", "secret")
		e.layouts(short, fullSha("a"), "flagd-sync")
		if err := e.push(func(o *PushOptions) { o.Names = []string{"flagd-sync"} }); err != nil {
			t.Fatal(err)
		}
		for _, r := range e.reg.Requests() {
			if r.Authorization != "Basic Ym90OnNlY3JldA==" {
				t.Fatalf("%s %s carried %q", r.Method, r.Target, r.Authorization)
			}
		}
	})
	t.Run("port: a bearer challenge is answered once per repository scope", func(t *testing.T) {
		e := newEnv(t)
		e.reg.Set(func(r *ocitest.Registry) { r.Token, r.User, r.Password = "tok", "bot", "secret" })
		t.Setenv("REGISTRY_USER", "bot")
		t.Setenv("REGISTRY_TOKEN", "secret")
		e.layouts(short, fullSha("a"), "flagd-sync", "flagd-cloud")
		if err := e.push(func(o *PushOptions) { o.Names = []string{"flagd-sync", "flagd-cloud"} }); err != nil {
			t.Fatal(err)
		}
		want := []string{"repository:open-feature-forking/flagd-sync:push,pull", "repository:open-feature-forking/flagd-cloud:push,pull"}
		if strings.Join(e.reg.TokenScopes(), "|") != strings.Join(want, "|") {
			t.Errorf("token scopes = %v, want %v", e.reg.TokenScopes(), want)
		}
		if e.reg.Tag("open-feature-forking/flagd-sync", "sha-"+short) == "" {
			t.Error("nothing was pushed after the token exchange")
		}
	})
	t.Run("port: a rejected login fails the push", func(t *testing.T) {
		e := newEnv(t)
		e.reg.Set(func(r *ocitest.Registry) { r.Token, r.User, r.Password = "tok", "bot", "secret" })
		t.Setenv("REGISTRY_USER", "bot")
		t.Setenv("REGISTRY_TOKEN", "wrong")
		e.layouts(short, fullSha("a"), "flagd-sync")
		if err := e.push(func(o *PushOptions) { o.Names = []string{"flagd-sync"} }); err == nil {
			t.Fatal("the push succeeded with a wrong token")
		}
	})
	t.Run("port: a blob the registry already has is not uploaded again", func(t *testing.T) {
		e := newEnv(t)
		e.layouts(short, fullSha("a"), "flagd-sync")
		opts := func(o *PushOptions) { o.Names = []string{"flagd-sync"} }
		if err := e.push(opts); err != nil {
			t.Fatal(err)
		}
		posts := func() int {
			n := 0
			for _, r := range e.reg.Requests() {
				if r.Method == "POST" {
					n++
				}
			}
			return n
		}
		first := posts()
		if first == 0 {
			t.Fatal("no upload")
		}
		if err := e.push(opts); err != nil {
			t.Fatal(err)
		}
		if posts() != first {
			t.Errorf("the second push started %d more uploads", posts()-first)
		}
	})
	t.Run("port: a digest the registry reports wrong stops the push", func(t *testing.T) {
		e := newEnv(t)
		e.layouts(short, fullSha("a"), "flagd-sync")
		e.reg.Set(func(r *ocitest.Registry) { r.ManifestDigest = func(string) string { return "" } })
		err := e.push(func(o *PushOptions) { o.Names = []string{"flagd-sync"} })
		if err == nil || !strings.Contains(err.Error(), "the registry stored") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("port: a name outside the seven is refused", func(t *testing.T) {
		e := newEnv(t)
		wantRefused(t, e.push(func(o *PushOptions) { o.Names = []string{"flagd"} }))
	})
}

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"no arguments", nil, 2, "usage: relay-ci publish"},
		{"unknown command", []string{"nope"}, 2, "unknown command"},
		{"short is seven hex digits", []string{"push", "dir", "--short", "abc12345"}, 1, "--short is seven hex digits"},
		{"short is lower case hex", []string{"push", "dir", "--short", "ABC1234"}, 1, "--short is seven hex digits"},
		{"move-main needs --sha", []string{"move-main", "--short", "abc1234"}, 2, "--sha"},
		{"push needs the directory", []string{"push", "--short", "abc1234"}, 2, "positional"},
		{"a refused name exits 3 with an error annotation", []string{"push", "dir", "--short", "abc1234", "--names", "flagd"}, 3, "::error::refused: flagd is not one of the images to publish"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			if got := Run(tt.args, &stdout, &stderr); got != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr %q)", got, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantErr)
			}
		})
	}
}
