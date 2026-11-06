package ghactions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommands(t *testing.T) {
	tests := []struct{ name, got, want string }{
		{"error without title", Error("", "boom"), "::error::boom"},
		{"error with title", Error("FAIL TestX", "pkg: bad"), "::error title=FAIL TestX::pkg: bad"},
		{"data escapes percent and newlines", Warning("", "100%\r\nnext\nline"), "::warning::100%25%0D%0Anext%0Aline"},
		{"title escapes colon and comma", Notice("a: b, c", "m"), "::notice title=a%3A b%2C c::m"},
		{"message keeps colon and comma", Error("", "a: b, c"), "::error::a: b, c"},
		{"escape order: percent first", Error("t%:", "x"), "::error title=t%25%3A::x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestOutputLine(t *testing.T) {
	if got, err := OutputLine("go", "true"); err != nil || got != "go=true\n" {
		t.Errorf("OutputLine = %q, %v", got, err)
	}
	for _, bad := range [][2]string{{"", "v"}, {"a=b", "v"}, {"a", "x\ny"}, {"a", "x\ry"}} {
		if _, err := OutputLine(bad[0], bad[1]); err == nil {
			t.Errorf("OutputLine(%q, %q) returned no error", bad[0], bad[1])
		}
	}
}

func TestAppendKeepsEarlierContent(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	sum := filepath.Join(t.TempDir(), "sum")
	t.Setenv("GITHUB_OUTPUT", out)
	t.Setenv("GITHUB_STEP_SUMMARY", sum)
	if err := os.WriteFile(out, []byte("first=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AppendOutput("go", "true"); err != nil {
		t.Fatal(err)
	}
	if err := AppendOutput("deps", "false"); err != nil {
		t.Fatal(err)
	}
	if err := AppendSummary("### Jobs\n"); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{out: "first=1\ngo=true\ndeps=false\n", sum: "### Jobs\n"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", filepath.Base(path), got, err, want)
		}
	}
}

func TestAppendWithoutTheVariableIsANoOp(t *testing.T) {
	t.Setenv("GITHUB_OUTPUT", "")
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	if err := AppendOutput("go", "true"); err != nil {
		t.Errorf("AppendOutput: %v", err)
	}
	if err := AppendSummary("x"); err != nil {
		t.Errorf("AppendSummary: %v", err)
	}
}

func TestAppendToAnUnwritablePathFails(t *testing.T) {
	t.Setenv("GITHUB_OUTPUT", filepath.Join(t.TempDir(), "no", "such", "dir", "out"))
	if err := AppendOutput("go", "true"); err == nil {
		t.Error("AppendOutput to a missing directory returned no error")
	}
}
