// Package headers reads the header settings of components that send HTTP requests. Each component words its own
// errors, so the errors here carry what went wrong and the caller turns them into its message.
package headers

import (
	"fmt"
	"maps"
	"strings"
)

// NotTextError reports a header whose value is not a string.
type NotTextError struct{ Name string }

func (e *NotTextError) Error() string {
	return fmt.Sprintf("the value of header %q is not a string", e.Name)
}

// NotMappingError reports a setting that is not a mapping of header names to values.
type NotMappingError struct{ Value any }

func (e *NotMappingError) Error() string {
	return fmt.Sprintf("expected a mapping of header names to strings, observed %T", e.Value)
}

// FromSetting reads a mapping of header names to values, given as map[string]string or as the map[string]any a
// document yields. A nil setting is no headers. The result is a copy; the names and values are not checked.
func FromSetting(raw any) (map[string]string, error) {
	switch h := raw.(type) {
	case nil:
		return nil, nil
	case map[string]string:
		return maps.Clone(h), nil
	case map[string]any:
		out := make(map[string]string, len(h))
		for name, value := range h {
			text, ok := value.(string)
			if !ok {
				return nil, &NotTextError{Name: name}
			}
			out[name] = text
		}
		return out, nil
	default:
		return nil, &NotMappingError{Value: raw}
	}
}

// ValidName reports whether name is a token, the grammar of an HTTP field name (RFC 9110, section 5.1).
func ValidName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.ContainsRune("!#$%&'*+-.^_`|~", c):
		default:
			return false
		}
	}
	return true
}
