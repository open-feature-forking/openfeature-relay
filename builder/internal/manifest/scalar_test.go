package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNonTextValues pins the problem a value that is not a text gives, for the dist, an entry and a replace.
func TestNonTextValues(t *testing.T) {
	for name, tt := range map[string]struct {
		manifest string
		want     []string
	}{
		"dist name is a list": {
			strings.Replace(valid, "name: flagd-custom", "name: [a]", 1),
			[]string{"2:3 dist: name is mandatory (BR-001)", `2:9 dist: "name" must be a text`},
		},
		"dist name is null": {
			strings.Replace(valid, "name: flagd-custom", "name: ~", 1),
			[]string{"2:3 dist: name is mandatory (BR-001)", `2:9 dist: "name" must be a text`},
		},
		"entry module is a mapping": {
			valid + "engines:\n  - {module: {a: b}, version: v1.0.0}\n",
			[]string{`18:14 engines[0]: "module" must be a text`},
		},
		"replace values": {
			valid + "replaces:\n  - {module: example.com/sync/file, path: null}\n  - {module: [x], path: ../x}\n",
			[]string{
				"18:5 replaces[0]: path is mandatory", `18:43 replaces[0]: "path" must be a text`,
				"19:5 replaces[1]: module is mandatory", `19:14 replaces[1]: "module" must be a text`,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tt.want, problems(t, tt.manifest))
		})
	}
}
