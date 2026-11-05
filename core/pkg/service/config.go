package service

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// ConfigDecoder decodes the plain-data Settings.Config of an endpoint: each key names the function that reads its
// value into the endpoint's own settings. A key it does not know is an error that names it and lists the keys.
type ConfigDecoder map[string]func(value any) error

// Decode applies every key of config. The keys in accept are valid but left to the caller.
func (d ConfigDecoder) Decode(config map[string]any, accept ...string) error {
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		decode, ok := d[key]
		if !ok {
			if slices.Contains(accept, key) {
				continue
			}
			return fmt.Errorf("unknown setting %q; the settings are: %s", key, strings.Join(d.keys(accept), ", "))
		}
		if err := decode(config[key]); err != nil {
			return fmt.Errorf("setting %q: %w", key, err)
		}
	}
	return nil
}

func (d ConfigDecoder) keys(accept []string) []string {
	keys := append([]string(nil), accept...)
	for key := range d {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Int64 reads a whole number given as any Go integer type, or as the float64 a JSON document yields.
func Int64(value any) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		return int64(v), nil //nolint:gosec // settings are small numbers
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case float64:
		if v == math.Trunc(v) && math.Abs(v) <= 1<<53 {
			return int64(v), nil
		}
	}
	return 0, fmt.Errorf("want a whole number, got %v (%T)", value, value)
}

// Uint16 reads a port.
func Uint16(value any) (uint16, error) {
	n, err := Int64(value)
	if err != nil {
		return 0, err
	}
	if n < 0 || n > math.MaxUint16 {
		return 0, fmt.Errorf("%d is not a port", n)
	}
	return uint16(n), nil
}

// Bool reads a boolean.
func Bool(value any) (bool, error) {
	b, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("want true or false, got %v (%T)", value, value)
	}
	return b, nil
}

// String reads a string.
func String(value any) (string, error) {
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("want a string, got %v (%T)", value, value)
	}
	return s, nil
}

// Strings reads a list of strings, given as []string or as the []any a JSON document yields.
func Strings(value any) ([]string, error) {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...), nil
	case []any:
		out := make([]string, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("want a list of strings, item %d is %v (%T)", i, item, item)
			}
			out[i] = s
		}
		return out, nil
	}
	return nil, fmt.Errorf("want a list of strings, got %v (%T)", value, value)
}

// Duration reads a duration given as a time.Duration or as a string such as "5s".
func Duration(value any) (time.Duration, error) {
	switch v := value.(type) {
	case time.Duration:
		return v, nil
	case string:
		d, err := time.ParseDuration(v)
		if err != nil {
			return 0, fmt.Errorf("want a duration such as 5s: %w", err)
		}
		return d, nil
	}
	return 0, fmt.Errorf("want a duration, got %v (%T)", value, value)
}

// Map reads a mapping with string keys and any values.
func Map(value any) (map[string]any, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("want a mapping, got %v (%T)", value, value)
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out, nil
}

// StringMap reads a mapping from strings to strings.
func StringMap(value any) (map[string]string, error) {
	switch v := value.(type) {
	case map[string]string:
		out := make(map[string]string, len(v))
		for k, s := range v {
			out[k] = s
		}
		return out, nil
	case map[string]any:
		out := make(map[string]string, len(v))
		for k, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("want strings, %q is %v (%T)", k, item, item)
			}
			out[k] = s
		}
		return out, nil
	}
	return nil, fmt.Errorf("want a mapping of strings, got %v (%T)", value, value)
}

// EndpointKeys point at where an endpoint keeps the settings every service endpoint reads: port, service-name,
// cors, context-values, max-request-header-bytes and max-request-body-bytes. A nil field leaves its key out.
type EndpointKeys struct {
	Port                  *uint16
	ServiceName           *string
	CORS                  *[]string
	ContextValues         *map[string]any
	MaxRequestHeaderBytes *int64
	MaxRequestBodyBytes   *int64
}

// Decoder returns a decoder of these keys and of the endpoint's own keys in own.
func (k EndpointKeys) Decoder(own ConfigDecoder) ConfigDecoder {
	d := make(ConfigDecoder, len(own)+6)
	addKey(d, "port", k.Port, Uint16)
	addKey(d, "service-name", k.ServiceName, String)
	addKey(d, "cors", k.CORS, Strings)
	addKey(d, "context-values", k.ContextValues, Map)
	addKey(d, "max-request-header-bytes", k.MaxRequestHeaderBytes, Int64)
	addKey(d, "max-request-body-bytes", k.MaxRequestBodyBytes, Int64)
	for key, decode := range own {
		d[key] = decode
	}
	return d
}

func addKey[T any](d ConfigDecoder, key string, dst *T, read func(any) (T, error)) {
	if dst != nil {
		d[key] = Set(dst, read)
	}
}

// Set returns a decoder for a key that stores the value read by read into dst.
func Set[T any](dst *T, read func(any) (T, error)) func(any) error {
	return func(value any) error {
		v, err := read(value)
		if err != nil {
			return err
		}
		*dst = v
		return nil
	}
}
