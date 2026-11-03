package builder

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

// ParseSources parse a json formatted SourceConfig array string and performs validations on the content
func ParseSources(sourcesFlag string) ([]sync.SourceConfig, error) {
	syncProvidersParsed := []sync.SourceConfig{}

	if err := json.Unmarshal([]byte(sourcesFlag), &syncProvidersParsed); err != nil {
		return syncProvidersParsed, fmt.Errorf("error parsing sync providers: %w", err)
	}
	for _, sp := range syncProvidersParsed {
		if sp.URI == "" {
			return syncProvidersParsed, errors.New("sync provider argument parse: uri is a required field")
		}
		if sp.Provider == "" {
			return syncProvidersParsed, errors.New("sync provider argument parse: provider is a required field")
		}
	}
	return syncProvidersParsed, nil
}

// ParseSyncProviderURIs uri flag based sync sources to SourceConfig array. Replaces uri prefixes where necessary to
// derive SourceConfig. Each URI is claimed by the first factory whose scheme it starts with; without factories, the
// official ones.
func ParseSyncProviderURIs(uris []string, factories ...sync.Factory) ([]sync.SourceConfig, error) {
	if len(factories) == 0 {
		factories = Official()
	}
	syncProvidersParsed := []sync.SourceConfig{}

	for _, uri := range uris {
		source, claimed := parseURI(uri, factories)
		if !claimed {
			return syncProvidersParsed, fmt.Errorf("invalid sync uri argument: %s, must start with 'file:', "+
				"'http(s)://', 'grpc(s)://', 'gs://', 'azblob://', 's3://' or 'core.openfeature.dev'", uri)
		}
		syncProvidersParsed = append(syncProvidersParsed, source)
	}
	return syncProvidersParsed, nil
}

func parseURI(uri string, factories []sync.Factory) (sync.SourceConfig, bool) {
	for _, f := range factories {
		if sf, ok := f.(sync.SchemeFactory); ok {
			if source, claimed := sf.ParseURI(uri); claimed {
				return source, true
			}
		}
	}
	return sync.SourceConfig{}, false
}
