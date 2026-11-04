package sse

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func parse(t *testing.T, stream string) []event {
	t.Helper()
	var got []event
	require.NoError(t, readEvents(strings.NewReader(stream), func(e event) { got = append(got, e) }))
	return got
}

func TestReadEvents(t *testing.T) {
	tests := map[string]struct {
		stream string
		want   []event
	}{
		"one event":                {"data: x\n\n", []event{{"message", "x"}}},
		"the event type":           {"event: update\ndata: x\n\n", []event{{"update", "x"}}},
		"data lines are joined":    {"data: a\ndata: b\n\n", []event{{"message", "a\nb"}}},
		"one space is stripped":    {"data:  x\n\n", []event{{"message", " x"}}},
		"no space after the colon": {"data:x\n\n", []event{{"message", "x"}}},
		"a field without a colon":  {"data\n\n", []event{{"message", ""}}},
		"two events":               {"data: 1\n\ndata: 2\n\n", []event{{"message", "1"}, {"message", "2"}}},
		"the type does not carry over": {
			"event: a\ndata: 1\n\ndata: 2\n\n", []event{{"a", "1"}, {"message", "2"}},
		},
		"carriage return and line feed": {"data: a\r\n\r\ndata: b\r\n\r\n", []event{{"message", "a"}, {"message", "b"}}},
		"carriage return alone":         {"data: a\r\rdata: b\r\r", []event{{"message", "a"}, {"message", "b"}}},
		"a byte order mark":             {"\ufeffdata: x\n\n", []event{{"message", "x"}}},
		"id and retry are read, not used": {
			"id: 7\nretry: 100\ndata: x\n\n", []event{{"message", "x"}},
		},
		"an unknown field": {"foo: bar\ndata: x\n\n", []event{{"message", "x"}}},
		"a comment":        {": hello\n\n", nil},
		"a keepalive":      {":\n\n:\n\n", nil},
		"a comment inside an event": {
			"data: a\n: note\ndata: b\n\n", []event{{"message", "a\nb"}},
		},
		"a comment between events": {"data: 1\n\n: ping\n\ndata: 2\n\n", []event{{"message", "1"}, {"message", "2"}}},
		"an event without data is not dispatched": {
			"event: update\n\ndata: x\n\n", []event{{"message", "x"}},
		},
		"blank lines alone":                             {"\n\n\n", nil},
		"an event cut off by the end":                   {"data: 1\n\ndata: 2", []event{{"message", "1"}}},
		"a final line without a terminator is no event": {"data: 1\n\ndata: 2\n", []event{{"message", "1"}}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tt.want, parse(t, tt.stream))
		})
	}
}

// A carriage return at the end of what has been read may be the first half of a CRLF: the line ends only once the next
// byte is known.
func TestCarriageReturnAtTheEndOfARead(t *testing.T) {
	advance, token, err := splitLines([]byte("data: x\r"), false)
	require.NoError(t, err)
	require.Zero(t, advance, "wait for the byte after the carriage return")
	require.Nil(t, token)

	advance, token, err = splitLines([]byte("data: x\r\ndata"), false)
	require.NoError(t, err)
	require.Equal(t, 9, advance)
	require.Equal(t, "data: x", string(token))

	advance, token, err = splitLines([]byte("data: x\r"), true)
	require.NoError(t, err)
	require.Equal(t, 8, advance)
	require.Equal(t, "data: x", string(token))
}

func TestAStreamInPiecesIsOneStream(t *testing.T) {
	r := &pieces{parts: []string{"da", "ta: a\r", "\n", "\r", "\ndata: b\n", "\n"}}
	var got []event
	require.NoError(t, readEvents(r, func(e event) { got = append(got, e) }))
	require.Equal(t, []event{{"message", "a"}, {"message", "b"}}, got)
}

type pieces struct{ parts []string }

func (p *pieces) Read(b []byte) (int, error) {
	if len(p.parts) == 0 {
		return 0, io.EOF
	}
	n := copy(b, p.parts[0])
	p.parts = p.parts[1:]
	return n, nil
}

func TestALineLongerThanTheLimitEndsTheStream(t *testing.T) {
	long := "data: " + strings.Repeat("x", maxLine) + "\n\n"
	err := readEvents(strings.NewReader(long), func(event) { t.Fatal("no event is dispatched") })
	require.ErrorIs(t, err, bufio.ErrTooLong)
}
