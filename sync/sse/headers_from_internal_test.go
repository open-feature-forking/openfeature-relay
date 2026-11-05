package sse

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/secret"
	"github.com/stretchr/testify/require"
)

// TestParseHeadersFrom pins the headersFrom setting for every Go type it accepts: the references it reads, or the
// exact error.
func TestParseHeadersFrom(t *testing.T) {
	want := map[string]secret.Ref{"X-Env": {Env: "TOKEN"}, "X-File": {File: "/run/token"}}
	for name, tt := range map[string]struct {
		raw  any
		want map[string]secret.Ref
		err  string
	}{
		"nil": {raw: nil},
		"refs": {
			raw:  map[string]secret.Ref{"X-Env": {Env: "TOKEN"}, "X-File": {File: "/run/token"}},
			want: want,
		},
		"string maps": {
			raw:  map[string]map[string]string{"X-Env": {"env": "TOKEN"}, "X-File": {"file": "/run/token"}},
			want: want,
		},
		"any with refs": {
			raw:  map[string]any{"X-Env": secret.Ref{Env: "TOKEN"}, "X-File": secret.Ref{File: "/run/token"}},
			want: want,
		},
		"any with string maps": {
			raw:  map[string]any{"X-Env": map[string]string{"env": "TOKEN"}, "X-File": map[string]string{"file": "/run/token"}},
			want: want,
		},
		"any with any maps": {
			raw:  map[string]any{"X-Env": map[string]any{"env": "TOKEN"}, "X-File": map[string]any{"file": "/run/token"}},
			want: want,
		},
		"both env and file": {
			raw:  map[string]any{"X-Key": map[string]any{"env": "TOKEN", "file": "/run/token"}},
			want: map[string]secret.Ref{"X-Key": {Env: "TOKEN", File: "/run/token"}},
		},
		"an empty ref": {
			raw:  map[string]any{"X-Key": map[string]any{}},
			want: map[string]secret.Ref{"X-Key": {}},
		},
		"empty refs":        {raw: map[string]secret.Ref{}},
		"empty string maps": {raw: map[string]map[string]string{}},
		"empty any":         {raw: map[string]any{}},

		"no mapping": {
			raw: []string{"x"},
			err: `the setting "headersFrom" must be a mapping of header names to {"env": NAME} or {"file": PATH}`,
		},
		"an entry no mapping": {
			raw: map[string]any{"X-Key": "TOKEN"},
			err: `the header "X-Key" in "headersFrom" must be a mapping with env or file`,
		},
		"env no text": {
			raw: map[string]any{"X-Key": map[string]any{"env": 1}},
			err: `the header "X-Key" in "headersFrom": "env" must be text`,
		},
		"file no text": {
			raw: map[string]any{"X-Key": map[string]any{"file": true}},
			err: `the header "X-Key" in "headersFrom": "file" must be text`,
		},
		"env no text beside an unknown key": {
			raw: map[string]any{"X-Key": map[string]any{"env": 1, "foo": "x"}},
			err: `the header "X-Key" in "headersFrom": "env" must be text`,
		},
		"an unknown key that is no text": {
			raw: map[string]any{"X-Key": map[string]any{"foo": 1}},
			err: `the header "X-Key" in "headersFrom": unknown key "foo" (allowed: env, file)`,
		},
		"an unknown key in a string map": {
			raw: map[string]map[string]string{"X-Key": {"foo": "x"}},
			err: `the header "X-Key" in "headersFrom": unknown key "foo" (allowed: env, file)`,
		},
		"an unknown key in an any string map": {
			raw: map[string]any{"X-Key": map[string]string{"foo": "x"}},
			err: `the header "X-Key" in "headersFrom": unknown key "foo" (allowed: env, file)`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseHeadersFrom(tt.raw)
			if tt.err != "" {
				require.EqualError(t, err, tt.err)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			if tt.want == nil {
				require.Nil(t, got, "no headers is a nil map, not an empty one")
			}
		})
	}
}

// TestRandomChoiceBetweenTwoFaultsIsKept pins a decided quirk (#177, Q2 (a)): with two faulty entries the reported
// one depends on the map iteration, so over many calls both are reported. Making the choice deterministic is a
// change of its own.
func TestRandomChoiceBetweenTwoFaultsIsKept(t *testing.T) {
	for name, tt := range map[string]struct {
		raw  any
		want []string
	}{
		"two headers with unknown keys": {
			raw: map[string]any{"X-A": map[string]any{"foo": "1"}, "X-B": map[string]any{"bar": "2"}},
			want: []string{
				`the header "X-A" in "headersFrom": unknown key "foo" (allowed: env, file)`,
				`the header "X-B" in "headersFrom": unknown key "bar" (allowed: env, file)`,
			},
		},
		"one header with two unknown keys": {
			raw: map[string]map[string]string{"X-A": {"foo": "1", "bar": "2"}},
			want: []string{
				`the header "X-A" in "headersFrom": unknown key "foo" (allowed: env, file)`,
				`the header "X-A" in "headersFrom": unknown key "bar" (allowed: env, file)`,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			seen := map[string]bool{}
			for range 1000 {
				_, err := parseHeadersFrom(tt.raw)
				require.Error(t, err)
				seen[err.Error()] = true
			}
			want := map[string]bool{}
			for _, text := range tt.want {
				want[text] = true
			}
			require.Equal(t, want, seen)
		})
	}
}
