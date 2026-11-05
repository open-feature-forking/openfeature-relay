package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigDecoder(t *testing.T) {
	var (
		port uint16
		on   bool
		wait time.Duration
		cors []string
	)
	d := ConfigDecoder{
		"port": Set(&port, Uint16), "enabled": Set(&on, Bool), "wait": Set(&wait, Duration), "cors": Set(&cors, Strings),
	}

	require.NoError(t, d.Decode(map[string]any{"port": 8013, "enabled": true, "wait": "5s", "cors": []any{"*"}}))
	require.Equal(t, uint16(8013), port)
	require.True(t, on)
	require.Equal(t, 5*time.Second, wait)
	require.Equal(t, []string{"*"}, cors)

	require.NoError(t, d.Decode(map[string]any{"port": float64(9), "wait": 2 * time.Second}))
	require.Equal(t, uint16(9), port)
	require.Equal(t, 2*time.Second, wait)

	require.EqualError(t, d.Decode(map[string]any{"prot": 1}), `unknown setting "prot"; the settings are: cors, enabled, port, wait`)
	require.EqualError(t, d.Decode(map[string]any{"port": 70000}), `setting "port": 70000 is not a port`)
	require.EqualError(t, d.Decode(map[string]any{"enabled": "yes"}), `setting "enabled": want true or false, got yes (string)`)
	require.NoError(t, d.Decode(map[string]any{"config": 1}, "config"), "an accepted key is left to the caller")
}

func TestConfigConversions(t *testing.T) {
	_, err := Int64(1.5)
	require.Error(t, err)
	m, err := StringMap(map[string]any{"a": "b"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"a": "b"}, m)
	_, err = StringMap(map[string]any{"a": 1})
	require.Error(t, err)
	_, err = Duration("soon")
	require.Error(t, err)
}

func TestInt64(t *testing.T) {
	for _, value := range []any{
		int(7), int8(7), int16(7), int32(7), int64(7), uint(7), uint8(7), uint16(7), uint32(7), float64(7),
	} {
		n, err := Int64(value)
		require.NoError(t, err, "%T", value)
		require.Equal(t, int64(7), n, "%T", value)
	}
	n, err := Int64(float64(-(1 << 53)))
	require.NoError(t, err)
	require.Equal(t, int64(-(1 << 53)), n)

	for _, value := range []any{float64(1<<53 + 2), uint64(7), float32(7), "7", nil} {
		_, err := Int64(value)
		require.Error(t, err, "%T", value)
	}
	_, err = Int64("7")
	require.EqualError(t, err, "want a whole number, got 7 (string)")
}

func TestUint16(t *testing.T) {
	p, err := Uint16(65535)
	require.NoError(t, err)
	require.Equal(t, uint16(65535), p)
	_, err = Uint16(-1)
	require.EqualError(t, err, "-1 is not a port")
	_, err = Uint16("8013")
	require.Error(t, err)
}

func TestString(t *testing.T) {
	s, err := String("flagd")
	require.NoError(t, err)
	require.Equal(t, "flagd", s)
	_, err = String(1)
	require.EqualError(t, err, "want a string, got 1 (int)")
}

func TestStrings(t *testing.T) {
	in := []string{"a", "b"}
	out, err := Strings(in)
	require.NoError(t, err)
	out[0] = "changed"
	require.Equal(t, []string{"a", "b"}, in, "the result is a copy")

	_, err = Strings([]any{"a", 1})
	require.EqualError(t, err, "want a list of strings, item 1 is 1 (int)")
	_, err = Strings("a")
	require.Error(t, err)
}

func TestMap(t *testing.T) {
	in := map[string]any{"a": 1}
	out, err := Map(in)
	require.NoError(t, err)
	out["b"] = 2
	require.Equal(t, map[string]any{"a": 1}, in, "the result is a copy")

	_, err = Map(map[string]string{"a": "b"})
	require.EqualError(t, err, "want a mapping, got map[a:b] (map[string]string)")
}

func TestStringMap(t *testing.T) {
	in := map[string]string{"a": "b"}
	out, err := StringMap(in)
	require.NoError(t, err)
	out["c"] = "d"
	require.Equal(t, map[string]string{"a": "b"}, in, "the result is a copy")

	_, err = StringMap(map[string]any{"a": 1})
	require.EqualError(t, err, `want strings, "a" is 1 (int)`)
	_, err = StringMap([]string{"a"})
	require.Error(t, err)
}

func TestDuration(t *testing.T) {
	d, err := Duration(time.Minute)
	require.NoError(t, err)
	require.Equal(t, time.Minute, d)
	_, err = Duration(5)
	require.EqualError(t, err, "want a duration, got 5 (int)")
}

func TestSetKeepsTheDestinationOnError(t *testing.T) {
	port := uint16(8013)
	require.Error(t, Set(&port, Uint16)("x"))
	require.Equal(t, uint16(8013), port)
}
