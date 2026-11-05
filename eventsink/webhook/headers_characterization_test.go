package webhook

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseHeadersErrors pins the error texts of the headers setting, which operators see at startup.
func TestParseHeadersErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  any
		want string
	}{
		"no mapping":       {[]string{"a"}, `setting "headers": expected a mapping of header names to strings, observed []string`},
		"value no string":  {map[string]any{"X-Key": 1}, `setting "headers": the value of header "X-Key" is not a string`},
		"empty name":       {map[string]string{"": "v"}, `setting "headers": "" is not a valid header name`},
		"name no token":    {map[string]string{"X(Key)": "v"}, `setting "headers": "X(Key)" is not a valid header name`},
		"name with colon":  {map[string]string{"X:Key": "v"}, `setting "headers": "X:Key" is not a valid header name`},
		"set by the sink":  {map[string]string{"content-type": "v"}, `setting "headers": the sink sets the header "content-type" itself`},
		"host":             {map[string]any{"Host": "h"}, `setting "headers": the sink sets the header "Host" itself`},
		"value line break": {map[string]string{"X-Key": "a\nb"}, `setting "headers": the value of header "X-Key" contains a line break`},
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

	h, err = parseHeaders(map[string]any{"x-key": "v", "Authorization": "Bearer t"})
	require.NoError(t, err)
	require.Equal(t, http.Header{"X-Key": {"v"}, "Authorization": {"Bearer t"}}, h)

	h, err = parseHeaders(map[string]string{})
	require.NoError(t, err)
	require.Equal(t, http.Header{}, h)
}
