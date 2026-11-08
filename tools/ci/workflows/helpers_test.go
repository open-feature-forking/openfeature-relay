// Package workflows pins the repository's workflow files, release configuration and CI shell scripts (K2, K3 and the
// publish gate). It is test-only: the checks read the repository at the root above this module.
//
// Translation decisions from the Python tests:
//   - every read goes through textio.ReadText (universal newlines), so a CRLF checkout reads as LF;
//   - \s, \d, \w and \b are ASCII in RE2 and Unicode in Python, which only makes a pattern stricter here;
//   - the job splitter is a line scanner: Python's lookahead and \Z do not exist in RE2.
package workflows

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/textio"
)

// root is the repository root: the nearest parent that holds the Makefile and the build workflow.
func root(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, ".github", "workflows", "build.yaml")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			skip(t, "the repository root is not above the working directory")
		}
		dir = parent
	}
}

// skip skips a test, except in CI, where a skipped check is a check that silently stopped running.
func skip(t *testing.T, why string) {
	t.Helper()
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Fatalf("would skip in CI: %s", why)
	}
	t.Skip(why)
}

// read is the normalized text of a file of the repository, given by its slash-separated path.
func read(t *testing.T, rel ...string) string {
	t.Helper()
	s, err := textio.ReadText(filepath.Join(append([]string{root(t)}, rel...)...))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// readAt is read for an absolute path.
func readAt(t *testing.T, path string) string {
	t.Helper()
	s, err := textio.ReadText(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// build and releaseCheck are the two workflows most checks read.
func build(t *testing.T) string { t.Helper(); return read(t, ".github", "workflows", "build.yaml") }
func releaseCheck(t *testing.T) string {
	t.Helper()
	return read(t, ".github", "workflows", "release-check.yaml")
}

// glob lists the files matching a pattern, sorted, like sorted(Path.glob(...)).
func glob(t *testing.T, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(m)
	return m
}

func workflowFiles(t *testing.T) []string {
	t.Helper()
	return glob(t, filepath.Join(root(t), ".github", "workflows", "*.y*ml"))
}

// before and after split the text at the first "\njobs:\n": the head (triggers, concurrency) and the jobs block.
func before(t *testing.T, text string) string {
	t.Helper()
	head, _, ok := strings.Cut(text, "\njobs:\n")
	if !ok {
		t.Fatal(`no "\njobs:\n" in the workflow`)
	}
	return head
}

func after(t *testing.T, text string) string {
	t.Helper()
	_, jobs, ok := strings.Cut(text, "\njobs:\n")
	if !ok {
		t.Fatal(`no "\njobs:\n" in the workflow`)
	}
	return jobs
}

// afterSep is the text after the first sep: strings.Split(s, sep, 1)[1], which fails where Python raises IndexError.
func afterSep(t *testing.T, s, sep string) string {
	t.Helper()
	_, b, ok := strings.Cut(s, sep)
	if !ok {
		t.Fatalf("%q is not in the text", sep)
	}
	return b
}

// beforeSep is the text before the first sep, or all of s without one: s.split(sep, 1)[0].
func beforeSep(s, sep string) string {
	a, _, _ := strings.Cut(s, sep)
	return a
}

// ---------------------------------------------------------------------------------------------------------------------
// The job splitter.

// headerStrict is a job's header line in the form job() reads; headerLoose is any 2-space key of the jobs block, in the
// form all_jobs() reads. A header ends with a newline.
var (
	headerStrict = regexp.MustCompile(`(?m)^  ([a-z][a-z0-9-]*):[ \t]*(?:#[^\n]*)?\n`)
	headerLoose  = regexp.MustCompile(`(?m)^  ([a-z][a-z0-9-]*):[^\n]*\n`)
)

type jobText struct{ name, text string }

// splitJobs cuts a jobs block at every header. A job runs from its header to the next one, or to the end of the block.
// withHeader keeps the header line in the text.
func splitJobs(jobs string, header *regexp.Regexp, withHeader bool) []jobText {
	loc := header.FindAllStringSubmatchIndex(jobs, -1)
	out := make([]jobText, 0, len(loc))
	for i, m := range loc {
		start := m[1]
		if withHeader {
			start = m[0]
		}
		end := len(jobs)
		if i+1 < len(loc) {
			end = loc[i+1][0]
		}
		out = append(out, jobText{jobs[m[2]:m[3]], jobs[start:end]})
	}
	return out
}

// job is the text of one top-level job of build.yaml, without its header line.
func job(t *testing.T, text, name string) string {
	t.Helper()
	for _, j := range splitJobs(after(t, text), headerStrict, false) {
		if j.name == name {
			return j.text
		}
	}
	t.Fatalf("job %s is missing", name)
	return ""
}

// allJobs is every job's text by id, header line included.
func allJobs(t *testing.T, text string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, j := range splitJobs(after(t, text), headerLoose, true) {
		m[j.name] = j.text
	}
	return m
}

// jobNames lists the ids of the jobs block in the form test_jobs reads: header lines with only a trailing comment.
func jobNames(t *testing.T, text string) []string {
	t.Helper()
	var names []string
	for _, m := range headerStrict.FindAllStringSubmatch(after(t, text), -1) {
		names = append(names, m[1])
	}
	return names
}

// ---------------------------------------------------------------------------------------------------------------------
// Assertions.

func assertIn(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%q is missing from the text", needle)
	}
}

func assertNotIn(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("%q is in the text", needle)
	}
}

func assertCount(t *testing.T, want int, haystack, needle string) {
	t.Helper()
	if got := strings.Count(haystack, needle); got != want {
		t.Errorf("%q occurs %d times, want %d", needle, got, want)
	}
}

func assertMatch(t *testing.T, haystack, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(haystack) {
		t.Errorf("the text does not match %s", pattern)
	}
}

func assertNoMatch(t *testing.T, haystack, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(haystack) {
		t.Errorf("the text matches %s", pattern)
	}
}

func assertEqualStrings(t *testing.T, want, got []string) {
	t.Helper()
	if !slices.Equal(want, got) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// indexOf is str.index: it fails where Python raises ValueError.
func indexOf(t *testing.T, s, sub string) int {
	t.Helper()
	i := strings.Index(s, sub)
	if i < 0 {
		t.Fatalf("%q is not in the text", sub)
	}
	return i
}

func assertBefore(t *testing.T, s, first, second string) {
	t.Helper()
	if a, b := indexOf(t, s, first), indexOf(t, s, second); a >= b {
		t.Errorf("%q (at %d) does not come before %q (at %d)", first, a, second, b)
	}
}

// pyLines is str.splitlines for text already free of "\r": it also breaks on \v, \f, \x1c-\x1e, \x85, U+2028 and U+2029.
func pyLines(s string) []string {
	var out []string
	start := 0
	for i, r := range s {
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, s[start:i])
			start = i + len(string(r))
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
