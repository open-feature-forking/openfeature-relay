package integration_test

import "testing"

func TestWithEnabledTags(t *testing.T) {
	for _, c := range []struct{ name, expr, enabled, want string }{
		{"none", "@rpc && ~@a && ~@b", "", "@rpc && ~@a && ~@b"},
		{"one", "@rpc && ~@a && ~@b", "@a", "@rpc && ~@b"},
		{"several, spaced", "@rpc && ~@a && ~@b && ~@c", "@a, @c", "@rpc && ~@b"},
		{"not in the expression", "@rpc && ~@a", "@z", "@rpc && ~@a"},
		{"a prefix is not the tag", "@rpc && ~@sync && ~@sync-port", "@sync-port", "@rpc && ~@sync"},
	} {
		if got := withEnabledTags(c.expr, c.enabled); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
