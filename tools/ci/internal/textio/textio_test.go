package textio

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"lf", "a\nb\n", "a\nb\n"},
		{"crlf", "a\r\nb\r\n", "a\nb\n"},
		{"lone cr", "a\rb\r", "a\nb\n"},
		{"mixed", "a\r\nb\rc\n", "a\nb\nc\n"},
		{"cr cr lf", "a\r\r\nb", "a\n\nb"},
		{"invalid utf-8", "a\xffb", "a�b"},
		// Python errors="replace" gives one U+FFFD per byte of the run; Go gives one for the whole run.
		{"invalid utf-8 run", "a\xe2\x82\xffb\xff\xffc", "a�b�c"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"one line no newline", "a", []string{"a"}},
		{"trailing newline", "a\nb\n", []string{"a", "b"}},
		{"blank line kept", "a\n\nb", []string{"a", "", "b"}},
		{"only a newline", "\n", []string{""}},
		{"form feed does not break", "a\x0cb\n", []string{"a\x0cb"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SplitLines(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitLines(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestReadTextTranslatesLineEndings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("one\r\ntwo\rthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadText(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "one\ntwo\nthree\n"; got != want {
		t.Errorf("ReadText = %q, want %q", got, want)
	}
}

func TestReadTextMissingFile(t *testing.T) {
	if _, err := ReadText(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("ReadText of a missing file returned no error")
	}
}

// bufio.Scanner would stop at 64 KiB.
func TestLinesKeepsALineOfOneMebibyte(t *testing.T) {
	long := strings.Repeat("x", 1<<20)
	got, err := Lines(strings.NewReader("a\r\n" + long + "\r\nb"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "a" || got[1] != long || got[2] != "b" {
		t.Errorf("Lines returned %d lines, the long one intact: %v", len(got), len(got) == 3 && got[1] == long)
	}
}
