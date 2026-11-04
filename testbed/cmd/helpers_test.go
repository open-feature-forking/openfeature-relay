package cmd

import (
	"github.com/open-feature-forking/openfeature-relay/runtime"
	runtimecmd "github.com/open-feature-forking/openfeature-relay/runtime/cmd"
)

// loadForTest loads a configuration as the testbed does and returns the URIs of its sources.
func loadForTest(b *bed, path string) ([]string, struct{}, error) {
	cfg, _, err := runtimecmd.LoadConfig(path, b.target.components, []runtimecmd.SourceRewrite{rewriteCombined})
	if err != nil {
		return nil, struct{}{}, err
	}
	var uris []string
	for _, s := range cfg.SyncProviders {
		uris = append(uris, s.URI)
	}
	return uris, struct{}{}, nil
}

// runtime returns the runtime in use.
func (t *target) runtime() *runtime.Runtime {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rt
}
