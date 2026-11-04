// Package secret resolves a value that a configuration names instead of holding: a credential in an environment
// variable or in a file, so that it appears neither in a manifest nor on a command line. Components that take
// credentials share the Ref form, `{"env": "NAME"}` or `{"file": "PATH"}`.
package secret

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Ref names where a secret comes from. Exactly one of Env and File is set.
type Ref struct {
	// Env is the name of an environment variable.
	Env string `json:"env,omitempty" mapstructure:"env"`
	// File is the path of a file that holds the secret; surrounding whitespace is dropped.
	File string `json:"file,omitempty" mapstructure:"file"`
}

// Resolve reads the secret. A variable that is unset or empty, and a file that cannot be read or is empty, are
// errors that name the variable or the file and never hold a secret value.
func (r Ref) Resolve() (string, error) {
	if (r.Env == "") == (r.File == "") {
		return "", errors.New("a secret names exactly one of env and file")
	}

	if r.Env != "" {
		value, ok := os.LookupEnv(r.Env)
		switch {
		case !ok:
			return "", fmt.Errorf("environment variable %q is not set", r.Env)
		case value == "":
			return "", fmt.Errorf("environment variable %q is empty", r.Env)
		}
		return value, nil
	}

	data, err := os.ReadFile(r.File)
	if err != nil {
		return "", fmt.Errorf("reading the secret: %w", err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("file %q is empty", r.File)
	}
	return value, nil
}

// String says where the secret comes from, for messages; it never holds the secret.
func (r Ref) String() string {
	if r.Env != "" {
		return fmt.Sprintf("environment variable %q", r.Env)
	}
	return fmt.Sprintf("file %q", r.File)
}
