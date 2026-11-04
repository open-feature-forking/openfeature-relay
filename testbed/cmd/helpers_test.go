package cmd

import (
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	runtimecmd "github.com/open-feature-forking/openfeature-relay/runtime/cmd"
)

// loadForTest loads a configuration as the testbed does and returns its sources.
func loadForTest(b *bed, path string) ([]coresync.SourceConfig, error) {
	cfg, _, err := runtimecmd.LoadConfig(path, b.target.components, []runtimecmd.SourceRewrite{rewriteCombined})
	if err != nil {
		return nil, err
	}
	return cfg.SyncProviders, nil
}

// runtime returns the runtime in use.
func (t *target) runtime() *runtime.Runtime {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rt
}
