package workflows

import "testing"

// Rows for the job splitter, which replaces a regexp with a lookahead and \Z (RE2 has neither).

const splitFixture = "name: x\njobs:\n" +
	"  alpha:\n    steps: a\n" +
	"  alpha-two: # a trailing comment\n    steps: b\n" +
	"  beta2:\n    steps: c\n" +
	"  last:\n    steps: d\n"

func TestJobSplitter(t *testing.T) {
	tests := []struct{ name, job, want string }{
		{"the first job", "alpha", "    steps: a\n"},
		{"a job name that is a prefix of another", "alpha-two", "    steps: b\n"},
		{"a header with a trailing comment ends the job before it and starts its own", "alpha", "    steps: a\n"},
		{"a name with a digit", "beta2", "    steps: c\n"},
		{"the last job runs to the end", "last", "    steps: d\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := job(t, splitFixture, tt.job); got != tt.want {
				t.Errorf("job(%s) = %q, want %q", tt.job, got, tt.want)
			}
		})
	}
	assertEqualStrings(t, []string{"alpha", "alpha-two", "beta2", "last"}, jobNames(t, splitFixture))
}

func TestAllJobsKeepsTheHeaderLine(t *testing.T) {
	all := allJobs(t, splitFixture)
	if len(all) != 4 {
		t.Fatalf("%d jobs, want 4", len(all))
	}
	if got, want := all["alpha-two"], "  alpha-two: # a trailing comment\n    steps: b\n"; got != want {
		t.Errorf("alpha-two = %q, want %q", got, want)
	}
	if got, want := all["last"], "  last:\n    steps: d\n"; got != want {
		t.Errorf("last = %q, want %q", got, want)
	}
}

func TestAJobHeaderNeedsTwoSpacesOfIndent(t *testing.T) {
	text := "jobs:\n  a:\n    b:\n      steps: x\n  c:\n    steps: y\n"
	assertEqualStrings(t, []string{"a", "c"}, jobNames(t, "x\n"+text))
}
