package sse

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseHeadersErrors pins the error texts of the headers setting, which operators see at startup, and the
// names it accepts: it checks less than the webhook event sink, and the HTTP client rejects the rest per request.
func TestParseHeadersErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  any
		want string
	}{
		"no mapping":       {[]string{"a"}, `the setting "headers" must be a mapping of header names to text`},
		"value no string":  {map[string]any{"X-Key": 1}, `the header "X-Key" must be text`},
		"empty name":       {map[string]string{"": "v"}, `a header name is empty`},
		"name with colon":  {map[string]string{"X:Key": "v"}, `the header "X:Key" is not valid: a name holds no space or colon, a value no line break`},
		"name with space":  {map[string]any{"X Key": "v"}, `the header "X Key" is not valid: a name holds no space or colon, a value no line break`},
		"value line break": {map[string]string{"X-Key": "a\r\nb"}, `the header "X-Key" is not valid: a name holds no space or colon, a value no line break`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseHeaders(tc.raw)
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestParseHeaders(t *testing.T) {
	h, err := parseHeaders(nil)
	require.NoError(t, err)
	require.Nil(t, h)

	h, err = parseHeaders(map[string]any{"X-Key": "v"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"X-Key": "v"}, h)

	h, err = parseHeaders(map[string]string{"X(Key)": "v", "Host": "h"})
	require.NoError(t, err, "a name that is no token, and a header the request sets itself, are accepted")
	require.Equal(t, map[string]string{"X(Key)": "v", "Host": "h"}, h)
}
