package sync

import (
	"fmt"
	"net/textproto"
	"sort"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/secret"
)

// HeaderSource names where the value of a header comes from: an environment variable or a file.
type HeaderSource = secret.Ref

// ResolveHeaders returns the headers of a source: its Headers, with the values of HeadersFrom read and merged over
// them. Names are in canonical form, so a header that both name is taken from HeadersFrom. A variable or file that
// cannot be read is an error naming the source and the header, never a value. A source without headers has none.
func ResolveHeaders(config SourceConfig) (map[string]string, error) {
	if len(config.Headers) == 0 && len(config.HeadersFrom) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(config.Headers)+len(config.HeadersFrom))
	for name, value := range config.Headers {
		headers[textproto.CanonicalMIMEHeaderKey(name)] = value
	}

	names := make([]string, 0, len(config.HeadersFrom))
	for name := range config.HeadersFrom {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value, err := config.HeadersFrom[name].Resolve()
		if err != nil {
			return nil, fmt.Errorf("source %q: header %q: %w", config.URI, name, err)
		}
		headers[textproto.CanonicalMIMEHeaderKey(name)] = value
	}
	return headers, nil
}
