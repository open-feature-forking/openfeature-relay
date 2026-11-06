// Package testfailures lists the failed tests of a go test log: test, package, time and first error, as a step summary
// and as annotations. It is report-only: the exit code is 0 whatever the log holds.
package testfailures

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/ghactions"
	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/textio"
)

// Python's \s, \S and \d are Unicode-aware and RE2's are ASCII-only; these classes keep the Python meaning.
const (
	ws = `\t-\r\x1c-\x1f\x85\p{Z}`
	sp = `[` + ws + `]`
	ns = `[^` + ws + `]`
	dg = `\p{Nd}`
)

var (
	timestamp    = regexp.MustCompile(`^` + ns + `+Z `)
	failLine     = regexp.MustCompile(`^` + sp + `*--- FAIL: (` + ns + `+) \(([` + dg + `.]+)s\)`)
	pkgFail      = regexp.MustCompile(`^FAIL\t(` + ns + `+)(?:` + sp + `+(.*` + ns + `))?`)
	other        = regexp.MustCompile(`^` + sp + `*(=== (RUN|CONT|PAUSE|NAME)|--- (PASS|SKIP)):? `)
	timeoutLine  = regexp.MustCompile(`^panic: test timed out after (` + ns + `+)`)
	runningLine  = regexp.MustCompile(`^` + sp + `+(Test` + ns + `+) \(`)
	errorLine    = regexp.MustCompile(`^` + sp + `+(Error|Messages):` + sp + `+(.*` + ns + `)`)
	continuation = regexp.MustCompile(`^` + sp + `+\t` + sp + `+(` + ns + `.*)$`)
	location     = regexp.MustCompile(`^` + sp + `+(` + ns + `+_test\.go:[` + dg + `]+): ?(.*)$`)
)

// Failure is one row of the report: a failed top-level test, or "-" for a package that failed without one.
type Failure struct {
	Test, Package, Secs, Err string
}

type record struct {
	name     string
	named    bool // the name of a timeout is set once a running test is found
	secs     string
	errText  string
	messages string
	logged   string
}

// field selects the Error or the Messages text of a record.
func (r *record) field(name string) *string {
	if name == "Error" {
		return &r.errText
	}
	return &r.messages
}

// splitLines splits as Python's str.splitlines does: on \n, \r, \r\n, \v, \f, \x1c-\x1e, \x85, U+2028 and U+2029.
func splitLines(text string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch r {
		case '\n', '\v', '\f', '\x1c', '\x1d', '\x1e', '\u0085', ' ', ' ':
			lines = append(lines, text[start:i])
			i += size
			start = i
		case '\r':
			lines = append(lines, text[start:i])
			i += size
			if i < len(text) && text[i] == '\n' {
				i++
			}
			start = i
		default:
			i += size
		}
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

// Failures returns the failed top-level tests of a log in log order. A package that failed without a failed test (a
// build failure, a panic) has the test "-". Without -v a test's output follows its FAIL line; with -v it precedes it, so
// the output since the last RUN or PASS line is kept for the FAIL line that may follow.
func Failures(log string) []Failure {
	var out []Failure
	var pending []*record
	var current, timeout *record
	before := &record{}
	field := "" // "Error", "Messages" or none

	for _, line := range splitLines(log) {
		line = timestamp.ReplaceAllString(line, "") // the timestamps of a downloaded Actions log
		if m := failLine.FindStringSubmatch(line); m != nil {
			name, _, _ := strings.Cut(m[1], "/")
			current = nil
			for _, p := range pending {
				if p.name == name {
					current = p
					break
				}
			}
			if current == nil {
				c := *before
				c.name, c.secs = name, m[2]
				current = &c
				pending = append(pending, current)
			}
			before, field = &record{}, ""
			continue
		}
		if other.MatchString(line) {
			current, before, field = nil, &record{}, ""
			continue
		}
		if m := timeoutLine.FindStringSubmatch(line); m != nil {
			timeout = &record{errText: "timed out after " + m[1]}
			continue
		}
		if timeout != nil && !timeout.named {
			if m := runningLine.FindStringSubmatch(line); m != nil {
				timeout.name, timeout.named = m[1], true
				continue
			}
		}
		target := current
		if target == nil {
			target = before
		}
		if m := errorLine.FindStringSubmatch(line); m != nil {
			p := target.field(m[1])
			if *p == "" {
				field = m[1]
				*p = m[2]
			} else {
				field = ""
			}
			continue
		}
		if field != "" {
			if m := continuation.FindStringSubmatch(line); m != nil {
				if p := target.field(field); strings.HasSuffix(*p, ":") {
					*p += " " + m[1]
				}
				field = ""
				continue
			}
		}
		field = ""
		// without an assertion's Error, the last located line: a test logs before it fails
		if m := location.FindStringSubmatch(line); m != nil && m[2] != "" {
			target.logged = m[1] + ": " + m[2]
		}
		if m := pkgFail.FindStringSubmatch(line); m != nil {
			if timeout != nil {
				if !timeout.named {
					timeout.name, timeout.named = "-", true
				}
				pending = append(pending, timeout)
			}
			rows := pending
			if len(rows) == 0 {
				rows = []*record{{name: "-", errText: m[2]}}
			}
			for _, f := range rows {
				text := f.errText
				if text == "" {
					text = f.logged
				}
				if f.messages != "" {
					text += ": " + f.messages
				}
				out = append(out, Failure{f.name, m[1], f.secs, text})
			}
			pending, current, before, field, timeout = nil, nil, &record{}, "", nil
		}
	}
	return out
}

// Report is the Markdown summary of the failures of one job. It ends with a newline.
func Report(rows []Failure, job string) string {
	head := "## Failed tests (" + job + ")\n\n"
	if len(rows) == 0 {
		return head + "No `--- FAIL` or `FAIL` package line in the log.\n"
	}
	var b strings.Builder
	b.WriteString(head)
	b.WriteString("| Test | Package | Time | First error |\n|---|---|---|---|\n")
	for _, r := range rows {
		errText := truncate(strings.ReplaceAll(r.Err, "|", `\|`), 160)
		took := ""
		if r.Secs != "" {
			took = r.Secs + " s"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n", r.Test, r.Package, took, errText)
	}
	return b.String()
}

// truncate keeps the first n characters, as a Python slice does.
func truncate(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	return string([]rune(text)[:n])
}

// Annotation is the ::error workflow command of a failure, its data and its title escaped as GitHub reads them.
func Annotation(name, pkg, errText string) string {
	return ghactions.Error("FAIL "+name, pkg+": "+errText)
}

// Run is the test-failures subcommand: --log, --job (default "test") and --summary. It exits 0 whatever the log holds.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("test-failures", flag.ContinueOnError)
	fs.SetOutput(stderr)
	logPath := fs.String("log", "", "the go test log")
	job := fs.String("job", "test", "the job's name, for the heading")
	summary := fs.String("summary", "", "a file the report is appended to")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *logPath == "" {
		_, _ = fmt.Fprintln(stderr, "test-failures: --log is required")
		return 2
	}
	var text string
	if _, err := os.Stat(*logPath); err == nil {
		if text, err = textio.ReadText(*logPath); err != nil {
			_, _ = fmt.Fprintf(stderr, "test-failures: %v\n", err)
		}
	}
	rows := Failures(text)
	report := Report(rows, *job)
	_, _ = io.WriteString(stdout, report+"\n")
	for _, r := range rows {
		_, _ = io.WriteString(stdout, Annotation(r.Test, r.Package, r.Err)+"\n")
	}
	if *summary != "" {
		if err := appendFile(*summary, report); err != nil {
			_, _ = fmt.Fprintf(stderr, "test-failures: %v\n", err)
		}
	}
	return 0
}

func appendFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(text)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
