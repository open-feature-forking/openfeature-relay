// Package ghactions writes what GitHub Actions reads from a step: the output and summary files, and workflow commands.
package ghactions

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// EscapeData escapes the message of a workflow command.
func EscapeData(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

// EscapeProperty escapes a property of a workflow command, such as its title.
func EscapeProperty(s string) string {
	return strings.NewReplacer(":", "%3A", ",", "%2C").Replace(EscapeData(s))
}

// Error is the ::error command of a message, with an optional title.
func Error(title, message string) string { return command("error", title, message) }

// Warning is the ::warning command of a message, with an optional title.
func Warning(title, message string) string { return command("warning", title, message) }

// Notice is the ::notice command of a message, with an optional title.
func Notice(title, message string) string { return command("notice", title, message) }

func command(kind, title, message string) string {
	if title == "" {
		return "::" + kind + "::" + EscapeData(message)
	}
	return "::" + kind + " title=" + EscapeProperty(title) + "::" + EscapeData(message)
}

// OutputLine is one name=value line of GITHUB_OUTPUT, newline included. The value must be a single line.
func OutputLine(name, value string) (string, error) {
	if name == "" || strings.ContainsAny(name+value, "\r\n") || strings.Contains(name, "=") {
		return "", fmt.Errorf("ghactions: %q=%q is not a single-line output", name, value)
	}
	return name + "=" + value + "\n", nil
}

// AppendToFile appends text to the file that the environment variable env names (GITHUB_OUTPUT, GITHUB_STEP_SUMMARY).
// It does nothing when the variable is unset or empty, as outside CI.
func AppendToFile(env, text string) error {
	path := os.Getenv(env)
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

// AppendOutput appends name=value to GITHUB_OUTPUT.
func AppendOutput(name, value string) error {
	line, err := OutputLine(name, value)
	if err != nil {
		return err
	}
	return AppendToFile("GITHUB_OUTPUT", line)
}

// AppendSummary appends text to GITHUB_STEP_SUMMARY.
func AppendSummary(text string) error { return AppendToFile("GITHUB_STEP_SUMMARY", text) }
