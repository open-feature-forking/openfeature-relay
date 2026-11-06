// Package textio reads text the way the Python helpers did: universal newlines and no decoding errors.
package textio

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// Normalize turns "\r\n" and then a lone "\r" into "\n", and replaces invalid UTF-8 with U+FFFD, as Python's
// universal-newline mode and errors="replace" do.
func Normalize(s string) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// Read returns everything r holds, normalized. Lines of any length are kept.
func Read(r io.Reader) (string, error) {
	b, err := io.ReadAll(bufio.NewReader(r))
	if err != nil {
		return "", err
	}
	return Normalize(string(b)), nil
}

// ReadText returns the contents of a file, normalized. Every read of a repository file goes through it, because a
// Windows checkout may hold CRLF.
func ReadText(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return Read(f)
}

// SplitLines splits normalized text on "\n". A final newline does not start another line, and empty text has none.
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// Lines reads r and returns its normalized lines.
func Lines(r io.Reader) ([]string, error) {
	s, err := Read(r)
	if err != nil {
		return nil, err
	}
	return SplitLines(s), nil
}
