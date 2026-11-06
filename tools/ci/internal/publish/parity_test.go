package publish

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/ocitest"
)

// Parity: until the cutover (slice X) the Python and the Go both exist. Each scenario runs the same commands once with
// publish-images.py and once with `relay-ci publish`, each against a registry of its own, and compares exit code,
// stdout, the refusal on stderr, the outputs file, everything the registry stored and every request it served.
// It skips without python3, and fails on a skip in GitHub Actions.

// pyScript is resolved at start-up: a test that changes directory would break a relative lookup.
var pyScript = ocitest.Script("publish-images.py")

type world struct {
	name string
	reg  *ocitest.Registry
	dir  string // holds the layouts and the outputs file
	py   bool
}

type step struct {
	args []string // "{layouts}" and "{out}" stand for the world's own paths
	env  map[string]string
	dir  string // working directory for move-main
	// loose compares a refusal without the ref it names: Python reads the refs of a layout from a set, in no fixed order,
	// so with two bad refs it names either.
	loose bool
}

type result struct {
	code           int
	stdout, stderr string
	outputs        string
}

func (w *world) run(t *testing.T, s step) result {
	t.Helper()
	repl := strings.NewReplacer("{layouts}", w.dir, "{out}", filepath.Join(w.dir, "outputs"))
	args := make([]string, len(s.args))
	for i, a := range s.args {
		args[i] = repl.Replace(a)
	}
	env := map[string]string{"REGISTRY_HOST": w.reg.Host(), "REGISTRY_USER": "", "REGISTRY_TOKEN": ""}
	for k, v := range s.env {
		env[k] = v
	}
	var r result
	if w.py {
		var list []string
		for k, v := range env {
			list = append(list, k+"="+v)
		}
		r.code, r.stdout, r.stderr = ocitest.RunPython(t, s.dir, list, "", append([]string{pyScript}, args...)...)
	} else {
		for k, v := range env {
			t.Setenv(k, v)
		}
		if s.dir != "" {
			t.Chdir(s.dir)
		}
		var stdout, stderr strings.Builder
		r.code = Run(args, &stdout, &stderr)
		r.stdout, r.stderr = stdout.String(), stderr.String()
	}
	r.stdout, r.stderr = strings.ReplaceAll(r.stdout, w.dir, "<dir>"), strings.ReplaceAll(r.stderr, w.dir, "<dir>")
	if raw, err := os.ReadFile(filepath.Join(w.dir, "outputs")); err == nil {
		r.outputs = string(raw)
	}
	return r
}

func newWorld(t *testing.T, name string, py bool) *world {
	return &world{name: name, reg: ocitest.NewRegistry(t), dir: t.TempDir(), py: py}
}

// parity runs setup in both worlds, then the steps, and compares everything after each step.
func parity(t *testing.T, setup func(*testing.T, *world), steps ...step) {
	t.Helper()
	ocitest.Python(t)
	py, goW := newWorld(t, "python", true), newWorld(t, "go", false)
	for _, w := range []*world{py, goW} {
		setup(t, w)
	}
	for i, s := range steps {
		a, b := py.run(t, s), goW.run(t, s)
		label := fmt.Sprintf("step %d %v", i, s.args)
		if a.code != b.code {
			t.Errorf("%s: exit code python %d, go %d\npython stderr: %s\ngo stderr: %s", label, a.code, b.code, a.stderr, b.stderr)
		}
		if a.stdout != b.stdout {
			t.Errorf("%s: stdout differs\npython:\n%s\ngo:\n%s", label, a.stdout, b.stdout)
		}
		if a.code == 3 && s.normalize(a.stderr) != s.normalize(b.stderr) {
			t.Errorf("%s: refusal differs\npython: %q\ngo:     %q", label, a.stderr, b.stderr)
		}
		if a.outputs != b.outputs {
			t.Errorf("%s: outputs differ\npython: %q\ngo:     %q", label, a.outputs, b.outputs)
		}
		if sa, sb := py.reg.Snapshot(), goW.reg.Snapshot(); sa != sb {
			t.Errorf("%s: the registries differ\npython:\n%s\ngo:\n%s", label, sa, sb)
		}
		if ra, rb := fmt.Sprintf("%q", py.reg.Requests()), fmt.Sprintf("%q", goW.reg.Requests()); ra != rb {
			t.Errorf("%s: the requests differ\npython:\n%s\ngo:\n%s", label, ra, rb)
		}
	}
}

func layoutsOf(refsFor func(name string) []string, names ...string) func(*testing.T, *world) {
	return func(t *testing.T, w *world) {
		if len(names) == 0 {
			names = Names
		}
		for _, n := range names {
			ocitest.WriteLayout(t, filepath.Join(w.dir, n+".tar"), ocitest.LayoutSpec{Refs: refsFor(n), Revision: fullSha("a")})
		}
	}
}

func shortRefs(name string) []string { return ocitest.RefsFor(name, short) }

func TestParityPush(t *testing.T) {
	push := step{args: []string{"push", "{layouts}", "--short", short, "--outputs", "{out}"}}
	t.Run("all seven images, with outputs", func(t *testing.T) { parity(t, layoutsOf(shortRefs), push) })
	t.Run("pushing twice uploads nothing the second time", func(t *testing.T) { parity(t, layoutsOf(shortRefs), push, push) })
	t.Run("a subset of the names", func(t *testing.T) {
		parity(t, layoutsOf(shortRefs), step{args: []string{"push", "{layouts}", "--short", short, "--names", "flagd-sync,flagd-cloud"}})
	})
	t.Run("a missing testbed layout is skipped", func(t *testing.T) {
		parity(t, layoutsOf(shortRefs, Names[:6]...), push)
	})
	t.Run("a missing distribution layout is refused", func(t *testing.T) {
		parity(t, layoutsOf(shortRefs, Names[1:]...), push)
	})
	t.Run("a layout that names a destination outside the seven is refused", func(t *testing.T) {
		parity(t, func(t *testing.T, w *world) {
			layoutsOf(shortRefs)(t, w)
			ocitest.WriteLayout(t, filepath.Join(w.dir, "flagd-sync.tar"), ocitest.LayoutSpec{
				Refs: []string{"ghcr.io/open-feature/flagd-sync:latest", "ghcr.io/open-feature/flagd:v1.2.3"}})
		}, step{args: push.args, loose: true})
	})
	t.Run("a layout of another image's repository is refused", func(t *testing.T) {
		parity(t, func(t *testing.T, w *world) {
			layoutsOf(shortRefs)(t, w)
			ocitest.WriteLayout(t, filepath.Join(w.dir, "flagd-sync.tar"), ocitest.LayoutSpec{Refs: []string{"ghcr.io/open-feature-forking/flagd-sync:main", "ghcr.io/open-feature-forking/flagd-cloud:sha-" + short}})
		}, push)
	})
	t.Run("a tag outside main and sha is refused", func(t *testing.T) {
		parity(t, func(t *testing.T, w *world) {
			layoutsOf(shortRefs)(t, w)
			ocitest.WriteLayout(t, filepath.Join(w.dir, "flagd-sync.tar"), ocitest.LayoutSpec{Refs: []string{
				"ghcr.io/open-feature-forking/flagd-sync:latest", "ghcr.io/open-feature-forking/flagd-sync:sha-" + short}})
		}, push)
	})
	t.Run("a name outside the seven is refused", func(t *testing.T) {
		parity(t, layoutsOf(shortRefs), step{args: []string{"push", "{layouts}", "--short", short, "--names", "flagd"}})
	})
	t.Run("a host that is neither ghcr.io nor local is refused", func(t *testing.T) {
		parity(t, layoutsOf(shortRefs), step{args: push.args, env: map[string]string{"REGISTRY_HOST": "registry.example.com"}})
	})
	t.Run("a local registry gets the Basic login", func(t *testing.T) {
		parity(t, layoutsOf(shortRefs), step{args: push.args, env: map[string]string{"REGISTRY_USER": "bot", "REGISTRY_TOKEN": "secret"}})
	})
	t.Run("a bearer challenge is answered with the Basic login", func(t *testing.T) {
		parity(t, func(t *testing.T, w *world) {
			layoutsOf(shortRefs)(t, w)
			w.reg.Set(func(r *ocitest.Registry) { r.Token, r.User, r.Password = "tok", "bot", "secret" })
		}, step{args: push.args, env: map[string]string{"REGISTRY_USER": "bot", "REGISTRY_TOKEN": "secret"}})
	})
	t.Run("a registry that reports another digest stops the push", func(t *testing.T) {
		parity(t, func(t *testing.T, w *world) {
			layoutsOf(shortRefs)(t, w)
			w.reg.Set(func(r *ocitest.Registry) {
				r.ManifestDigest = func(string) string { return "sha256:" + strings.Repeat("0", 64) }
			})
		}, push)
	})
}

// moveSetup publishes the layouts of two commits of a shared repository (flagd-sync only) in a world, then moves :main to
// the old one unless noMain.
type history struct {
	repo                   *repo
	old, next, oldRevision string
}

func newHistory(t *testing.T, revisionOfOld string) history {
	r := newRepo(t)
	h := history{repo: r, old: r.commit("old")}
	h.next = r.commit("new")
	switch revisionOfOld {
	case "":
		h.oldRevision = h.old
	case "side":
		h.oldRevision = r.git("commit-tree", r.git("rev-parse", "HEAD^{tree}"), "-p", h.old, "-m", "side")
	default:
		h.oldRevision = revisionOfOld
	}
	return h
}

func (h history) sha() (old, next, oldRevision string) { return h.old, h.next, h.oldRevision }

func (h history) setup(published ...string) func(*testing.T, *world) {
	old, next, oldRevision := h.sha()
	return func(t *testing.T, w *world) {
		for _, c := range []struct{ sha, revision string }{{old, oldRevision}, {next, next}} {
			if !contains(published, c.sha) {
				continue
			}
			dir := filepath.Join(w.dir, "layouts-"+c.sha[:7])
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			ocitest.WriteLayout(t, filepath.Join(dir, "flagd-sync.tar"), ocitest.LayoutSpec{Refs: ocitest.RefsFor("flagd-sync", c.sha[:7]), Revision: c.revision})
			t.Setenv("REGISTRY_HOST", w.reg.Host())
			if err := Push(PushOptions{Layouts: dir, Short: c.sha[:7], Names: []string{"flagd-sync"}}, func(string) {}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestParityMoveMain(t *testing.T) {
	move := func(h history, sha string, force bool) step {
		args := []string{"move-main", "--short", sha[:7], "--sha", sha, "--names", "flagd-sync"}
		if force {
			args = append(args, "--force")
		}
		return step{args: args, dir: h.repo.dir}
	}
	t.Run("main moves forward and an older commit published later leaves it alone", func(t *testing.T) {
		h := newHistory(t, "")
		old, next, _ := h.sha()
		parity(t, h.setup(old, next), move(h, old, false), move(h, next, false), move(h, next, false), move(h, old, false))
	})
	t.Run("a main that holds a commit this checkout lacks stays until forced", func(t *testing.T) {
		h := newHistory(t, fullSha("f"))
		old, next, _ := h.sha()
		parity(t, h.setup(old, next), move(h, old, false), move(h, next, false), move(h, next, true))
	})
	t.Run("force does not move main back to a commit the checkout has", func(t *testing.T) {
		h := newHistory(t, "side")
		old, next, _ := h.sha()
		parity(t, h.setup(old, next), move(h, old, false), move(h, next, false), move(h, next, true))
	})
	t.Run("an image that was not published keeps main", func(t *testing.T) {
		h := newHistory(t, "")
		old, next, _ := h.sha()
		parity(t, h.setup(old), move(h, old, false), move(h, next, false))
	})
	t.Run("every name, the ones without an image kept", func(t *testing.T) {
		h := newHistory(t, "")
		old, _, _ := h.sha()
		parity(t, h.setup(old), step{args: []string{"move-main", "--short", old[:7], "--sha", old}, dir: h.repo.dir})
	})
	t.Run("a name outside the seven is refused", func(t *testing.T) {
		h := newHistory(t, "")
		old, _, _ := h.sha()
		parity(t, h.setup(old), step{args: []string{"move-main", "--short", old[:7], "--sha", old, "--names", "flagd"}, dir: h.repo.dir})
	})
	t.Run("a host that is neither ghcr.io nor local is refused", func(t *testing.T) {
		h := newHistory(t, "")
		old, _, _ := h.sha()
		parity(t, h.setup(old), step{args: []string{"move-main", "--short", old[:7], "--sha", old}, dir: h.repo.dir,
			env: map[string]string{"REGISTRY_HOST": "registry.example.com"}})
	})
	t.Run("a registry that reports another digest for main is an error", func(t *testing.T) {
		h := newHistory(t, "")
		old, _, _ := h.sha()
		setup := h.setup(old)
		parity(t, func(t *testing.T, w *world) {
			setup(t, w)
			w.reg.Set(func(r *ocitest.Registry) {
				r.ManifestDigest = func(string) string { return "sha256:" + strings.Repeat("0", 64) }
			})
		}, move(h, old, false))
	})
}

var namedRef = regexp.MustCompile(`the layout names \S+, which`)

func (s step) normalize(stderr string) string {
	if s.loose {
		return namedRef.ReplaceAllString(stderr, "the layout names REF, which")
	}
	return stderr
}
