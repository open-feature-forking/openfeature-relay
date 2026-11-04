package flagsmithmiddleware

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Reference names where a secret comes from: {"env": "<VAR>"} or {"file": "<path>"}.
const (
	refEnv  = "env"
	refFile = "file"
)

// resolveSecret reads the secret a reference names. The error names the reference, never the value.
func resolveSecret(ref any) (string, error) {
	m, ok := ref.(map[string]any)
	if !ok || len(m) != 1 {
		return "", errors.New(`a key is written as {"env": "<VAR>"} or {"file": "<path>"}, not in the configuration`)
	}
	for kind, v := range m {
		name, ok := v.(string)
		if !ok || name == "" {
			return "", fmt.Errorf("%q is not a name", kind)
		}
		switch kind {
		case refEnv:
			value, set := os.LookupEnv(name)
			if !set || value == "" {
				return "", fmt.Errorf("the environment variable %q is not set or empty", name)
			}
			return value, nil
		case refFile:
			data, err := os.ReadFile(name)
			if err != nil {
				return "", fmt.Errorf("the file %q cannot be read: %w", name, err)
			}
			value := strings.TrimSpace(string(data))
			if value == "" {
				return "", fmt.Errorf("the file %q is empty", name)
			}
			return value, nil
		}
		return "", fmt.Errorf("unknown reference %q: use %q or %q", kind, refEnv, refFile)
	}
	return "", nil
}
