// Package flagargs parses the command lines of the relay-ci subcommands the way the Python helpers' argparse did: flags
// and positional arguments may be mixed in any order, and a flag takes its value as the next argument or after "=".
package flagargs

import (
	"fmt"
	"strings"
)

// Spec names what a subcommand accepts.
type Spec struct {
	Values      []string // flags that take a value, without the leading "--"
	Bools       []string // flags that take none
	Required    []string // value flags that must be given
	Positionals int      // exact number of positional arguments
}

// Result is a parsed command line.
type Result struct {
	Positional []string
	values     map[string]string
	bools      map[string]bool
}

// Value returns the value of a flag, or the empty string when it was not given.
func (r Result) Value(name string) string { return r.values[name] }

// Given reports whether a value flag was given.
func (r Result) Given(name string) bool { _, ok := r.values[name]; return ok }

// Bool reports whether a boolean flag was given.
func (r Result) Bool(name string) bool { return r.bools[name] }

// Parse reads args against spec. The error text is meant for stderr.
func Parse(args []string, spec Spec) (Result, error) {
	res := Result{values: map[string]string{}, bools: map[string]bool{}}
	isValue, isBool := set(spec.Values), set(spec.Bools)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") || arg == "--" {
			res.Positional = append(res.Positional, arg)
			continue
		}
		name, value, hasValue := strings.Cut(arg[2:], "=")
		switch {
		case isValue[name]:
			if !hasValue {
				if i+1 >= len(args) {
					return res, fmt.Errorf("argument --%s: expected one argument", name)
				}
				i++
				value = args[i]
			}
			res.values[name] = value
		case isBool[name]:
			if hasValue {
				return res, fmt.Errorf("argument --%s: ignored explicit argument %q", name, value)
			}
			res.bools[name] = true
		default:
			return res, fmt.Errorf("unrecognized arguments: %s", arg)
		}
	}
	var missing []string
	for _, name := range spec.Required {
		if !res.Given(name) {
			missing = append(missing, "--"+name)
		}
	}
	if len(missing) > 0 {
		return res, fmt.Errorf("the following arguments are required: %s", strings.Join(missing, ", "))
	}
	if len(res.Positional) != spec.Positionals {
		return res, fmt.Errorf("expected %d positional argument(s), got %d", spec.Positionals, len(res.Positional))
	}
	return res, nil
}

func set(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}
