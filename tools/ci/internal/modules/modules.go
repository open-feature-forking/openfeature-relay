// Package modules decides which gated jobs of build.yaml a change calls for: the flags of `relay-ci modules changes`.
//
// It is the port of the `changes` command of .github/scripts/ci-modules.py, with the scope of plan step 18, Q8 (b):
// the flags, the base selection and the check that every module directory has an entry in ci-modules.json. The
// module matrix (`test`, `all`, `lint-all`) that nothing read is not ported.
package modules

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/ghactions"
	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/textio"
)

// Zero is the all-zero commit that GitHub sends as `before` of a push that creates a branch.
const Zero = "0000000000000000000000000000000000000000"

// Flag says which changed paths call for a gated job of build.yaml; the job runs when its flag is true. A path counts
// when Include matches it and Exclude does not, or when Extra matches it. RE2 has no lookahead, so the exclusion is a
// second expression. The patterns are ASCII: \s, \d and \S do not match Unicode, as they do in Python.
type Flag struct {
	Name    string
	Include *regexp.Regexp
	Exclude *regexp.Regexp
	Extra   *regexp.Regexp
}

// toolsCI is the repository's own CI tooling: its sources and module files do not call for a gated job.
const toolsCI = `^tools/ci/`

func newFlag(name, include, extra string) Flag {
	f := Flag{Name: name, Include: regexp.MustCompile(include), Exclude: regexp.MustCompile(toolsCI)}
	if extra != "" {
		f.Extra = regexp.MustCompile(extra)
	}
	return f
}

// Flags is in the order of the GITHUB_OUTPUT lines and of the summary.
var Flags = []Flag{
	newFlag("go", `\.go$|(^|/)go\.(mod|sum)$`, ""),
	newFlag("deps", `(^|/)go\.(mod|sum)$`, ""),
	newFlag("dockerfile", `(^|/)[^/]*Dockerfile$`, ""),
	newFlag("benchmark", `\.go$|(^|/)go\.(mod|sum)$|^\.github/scripts/(run-benchmarks|compare-allocs)`,
		`^tools/ci/internal/allocs/|^\.github/scripts/run-benchmarks`),
	newFlag("image", `(^|/)go\.(mod|sum)$|(^|/)[^/]*Dockerfile$|^\.github/workflows/build\.yaml$`, ""),
	// the reference Dockerfile of the Builder and the tests that build images from it
	newFlag("reference", `^builder/reference/|^test/image/|(^|/)[^/]*Dockerfile$|^\.github/workflows/build\.yaml$`, ""),
	// Dockerfiles; the manifest of flagd-full and the Builder that builds it; flagd, which holds flagd-full's main and
	// its list of components; the differential's own test; any Go source or module file, because a change under core/,
	// service/, sync/, runtime/ or evaluator/ changes flagd-full.
	newFlag("upgrade", `\.go$|(^|/)go\.(mod|sum)$|(^|/)[^/]*Dockerfile$|^distributions/|^builder/|^flagd/|^test/upgrade/|`+
		`^\.github/workflows/build\.yaml$`, ""),
	// the same, and the testbed, its distribution and the suites
	newFlag("testbed", `\.go$|(^|/)go\.(mod|sum)$|(^|/)[^/]*Dockerfile$|^distributions/|^builder/|^flagd/|^testbed/|`+
		`^test/(testbed|integration|upgrade)/|^\.github/scripts/(testbed|compare-pass)|^\.github/workflows/build\.yaml$`,
		`^tools/ci/internal/passsets/|^\.github/scripts/testbed`),
}

// counts reports whether a changed path sets the flag. Python's `$` also matches before a final newline, so one is
// dropped first.
func (f Flag) counts(path string) bool {
	path = strings.TrimSuffix(path, "\n")
	if f.Include.MatchString(path) && !f.Exclude.MatchString(path) {
		return true
	}
	return f.Extra != nil && f.Extra.MatchString(path)
}

// Values is the value of each flag, in the order of Flags. With all set, every flag is true.
func Values(changed []string, all bool) []bool {
	out := make([]bool, len(Flags))
	for i, f := range Flags {
		out[i] = all || slices.ContainsFunc(changed, f.counts)
	}
	return out
}

// FlagLines is the GITHUB_OUTPUT text of the flags.
func FlagLines(values []bool) string {
	var b strings.Builder
	for i, f := range Flags {
		fmt.Fprintf(&b, "%s=%t\n", f.Name, values[i])
	}
	return b.String()
}

// TableError is a module directory without an entry in ci-modules.json: a mistake to fix, not to run around.
type TableError struct{ Missing []string }

func (e *TableError) Error() string {
	return "no entry in ci-modules.json for: " + strings.Join(e.Missing, ", ")
}

var pruneRE = regexp.MustCompile(`-path\s+("[^"]+"|\S+)`)

// ParsePrune is the -path terms of the ALL_GO_MOD_DIRS line of the Makefile.
func ParsePrune(makefile string) ([]string, error) {
	for _, line := range textio.SplitLines(makefile) {
		if !strings.HasPrefix(line, "ALL_GO_MOD_DIRS") {
			continue
		}
		var out []string
		for _, m := range pruneRE.FindAllStringSubmatch(line, -1) {
			out = append(out, strings.Trim(m[1], `"`))
		}
		return out, nil
	}
	return nil, errors.New("the Makefile has no ALL_GO_MOD_DIRS line")
}

// Fnmatch is Python's fnmatch.fnmatchcase: `*` matches any run of characters, `/` included.
func Fnmatch(name, pattern string) bool {
	var b strings.Builder
	b.WriteString(`(?s)^`)
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			b.WriteString(`.*`)
		case '?':
			b.WriteString(`.`)
		case '[':
			j := i + 1
			if j < len(pattern) && pattern[j] == '!' {
				j++
			}
			if j < len(pattern) && pattern[j] == ']' {
				j++
			}
			for j < len(pattern) && pattern[j] != ']' {
				j++
			}
			if j >= len(pattern) {
				b.WriteString(`\[`)
				continue
			}
			set := strings.ReplaceAll(pattern[i+1:j], `\`, `\\`)
			switch {
			case strings.HasPrefix(set, "!"):
				set = "^" + set[1:]
			case strings.HasPrefix(set, "^"):
				set = `\` + set
			}
			b.WriteString("[" + set + "]")
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`$`)
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(name)
}

// ModuleDirs are the directories of files (repository-relative, slash-separated) that hold a go.mod and are not under
// a pruned path. The pruned paths apply to every ancestor, so a nested module is pruned with its parent.
func ModuleDirs(files, prune []string) []string {
	seen := map[string]bool{}
	for _, f := range files {
		if f == "go.mod" || !strings.HasSuffix(f, "/go.mod") {
			continue
		}
		d := strings.TrimSuffix(f, "/go.mod")
		parts := strings.Split(d, "/")
		pruned := false
		for i := range parts {
			a := "./" + strings.Join(parts[:i+1], "/")
			if slices.ContainsFunc(prune, func(p string) bool { return Fnmatch(a, p) }) {
				pruned = true
				break
			}
		}
		if !pruned {
			seen[d] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	slices.Sort(out)
	return out
}

// ListFiles is every file under root as a slash-separated relative path, without the .git directories.
func ListFiles(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(rel))
		return nil
	})
	return found, err
}

// CheckTable fails with a *TableError when a module directory has no entry in the table's "modules".
func CheckTable(dirs []string, tableJSON string) error {
	var table struct {
		Modules map[string]json.RawMessage `json:"modules"`
	}
	if err := json.Unmarshal([]byte(tableJSON), &table); err != nil {
		return err
	}
	if table.Modules == nil {
		return errors.New(`ci-modules.json has no "modules"`)
	}
	var missing []string
	for _, d := range dirs {
		if _, ok := table.Modules[d]; !ok {
			missing = append(missing, d)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return &TableError{Missing: missing}
	}
	return nil
}

// git runs git in root. The error is set only when git cannot be started; a non-zero exit is the code.
func git(root string, args ...string) (stdout []byte, code int, err error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	runErr := cmd.Run()
	var exit *exec.ExitError
	switch {
	case runErr == nil:
		return out.Bytes(), 0, nil
	case errors.As(runErr, &exit):
		return out.Bytes(), exit.ExitCode(), nil
	default:
		return nil, -1, runErr
	}
}

// ChangeSet is the paths a change touches for the flags. All is true when every flag is true: FULL_CI, a manual run,
// the schedule and a base that cannot be compared with. A pull request is its merge commit against the first parent,
// a push is before..after, on main as on any other branch.
func ChangeSet(root, event, sha, before, fullCI string) (changed []string, all bool, err error) {
	if fullCI == "true" || (event != "pull_request" && event != "push") {
		return nil, true, nil
	}
	var base string
	if event == "pull_request" {
		out, _, err := git(root, "rev-parse", "--verify", "-q", sha+"^1")
		if err != nil {
			return nil, false, err
		}
		base = strings.TrimSpace(string(out))
	} else {
		base = strings.TrimSpace(before)
		if base == Zero {
			base = ""
		} else {
			_, code, err := git(root, "cat-file", "-e", base+"^{commit}")
			if err != nil {
				return nil, false, err
			}
			if code != 0 {
				base = ""
			}
		}
	}
	if base == "" {
		return nil, true, nil
	}
	out, code, err := git(root, "diff", "--name-only", "--no-renames", "-z", base, sha)
	if err != nil {
		return nil, false, err
	}
	if code != 0 {
		return nil, false, fmt.Errorf("git diff %s %s exited %d", base, sha, code)
	}
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		if !utf8.Valid(p) { // Python decodes the paths strictly, so such a path is a failure that falls open
			return nil, false, fmt.Errorf("the path %q is not UTF-8", p)
		}
		changed = append(changed, string(p))
	}
	return changed, false, nil
}

// Options are the arguments of `modules changes`.
type Options struct {
	Root, Event, Ref, SHA, Before string
	GithubOutput, Summary         string
}

const summaryHead = "## Jobs this change calls for\n\n"

// Decide computes the flag values and the summary of a change. It returns a *TableError for a module directory
// without a table entry, and any other error for a failure of the computation.
func Decide(o Options, fullCI string) (values []bool, summary string, err error) {
	makefile, err := textio.ReadText(filepath.Join(o.Root, "Makefile"))
	if err != nil {
		return nil, "", err
	}
	prune, err := ParsePrune(makefile)
	if err != nil {
		return nil, "", err
	}
	table, err := textio.ReadText(filepath.Join(o.Root, ".github", "scripts", "ci-modules.json"))
	if err != nil {
		return nil, "", err
	}
	files, err := ListFiles(o.Root)
	if err != nil {
		return nil, "", err
	}
	if err := CheckTable(ModuleDirs(files, prune), table); err != nil {
		return nil, "", err
	}
	changed, all, err := ChangeSet(o.Root, o.Event, o.SHA, o.Before, fullCI)
	if err != nil {
		return nil, "", err
	}
	values = Values(changed, all)
	var b strings.Builder
	b.WriteString(summaryHead)
	for i, f := range Flags {
		answer := "no"
		if values[i] {
			answer = "yes"
		}
		fmt.Fprintf(&b, "- `%s`: %s\n", f.Name, answer)
	}
	return values, b.String(), nil
}

func appendTo(path, text string) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(text)
	return errors.Join(werr, f.Close())
}

// decide is Decide with a panic turned into an error, so that it falls open like any other failure.
func decide(o Options, fullCI string) (values []bool, summary string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return Decide(o, fullCI)
}

const usage = "usage: relay-ci modules [--root DIR] changes --event E --sha S [--ref R] [--before B] " +
	"[--github-output FILE] [--summary FILE]\n"

// Run is `relay-ci modules`. Exit codes: 0 when it decided or fell open, 1 for a module directory without a table
// entry, 2 for a usage error or when even the fall-open output cannot be written.
//
// A bug in the computation must not skip a gated job: every flag is true instead, and the run says so.
func Run(args []string, stdout, stderr io.Writer) int {
	o := Options{Root: "."}
	set := flag.NewFlagSet("relay-ci modules changes", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.StringVar(&o.Root, "root", ".", "the repository root")
	set.StringVar(&o.Event, "event", "", "the event name (required)")
	set.StringVar(&o.Ref, "ref", "", "the ref (accepted, not read)")
	set.StringVar(&o.SHA, "sha", "", "the commit (required)")
	set.StringVar(&o.Before, "before", "", "the commit before a push")
	set.StringVar(&o.GithubOutput, "github-output", "", "the file the flags are appended to")
	set.StringVar(&o.Summary, "summary", "", "the file the summary is appended to")
	// `--root R` may come before the subcommand, as it did for the Python.
	if len(args) >= 2 && args[0] == "--root" {
		o.Root, args = args[1], args[2:]
	}
	if len(args) == 0 || args[0] != "changes" {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	if err := set.Parse(args[1:]); err != nil {
		return 2
	}
	if set.NArg() != 0 || o.Event == "" || o.SHA == "" {
		_, _ = io.WriteString(stderr, "relay-ci modules changes: --event and --sha are required, and no argument may follow\n"+usage)
		return 2
	}

	values, summary, err := decide(o, os.Getenv("FULL_CI"))
	var tableErr *TableError
	switch {
	case errors.As(err, &tableErr):
		_, _ = fmt.Fprintln(stdout, ghactions.Error("", tableErr.Error()))
		return 1
	case err != nil:
		_, _ = fmt.Fprintln(stdout, ghactions.Warning("", fmt.Sprintf("relay-ci failed (%v); every gated job runs", err)))
		values = Values(nil, true)
		summary = summaryHead + fmt.Sprintf("All: relay-ci failed with `%v`.\n", err)
	}
	_, _ = fmt.Fprintln(stdout, summary)
	if err := errors.Join(appendTo(o.Summary, summary), appendTo(o.GithubOutput, FlagLines(values))); err != nil {
		_, _ = fmt.Fprintf(stderr, "relay-ci: %v\n", err)
		return 2
	}
	return 0
}
