//go:build unix

package runtime

import (
	"context"
	"errors"
	"github.com/open-feature-forking/openfeature-relay/runtime/internal/officialset"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const rejectingType = "rejecting"

type rejectingFactory struct{}

func (rejectingFactory) Type() string { return rejectingType }

func (rejectingFactory) Create(configformat.Settings) (configformat.ConfigurationFormat, error) {
	return rejectingConfigurationFormat{}, nil
}

type rejectingConfigurationFormat struct{}

func (rejectingConfigurationFormat) Type() string { return rejectingType }

func (rejectingConfigurationFormat) Accept(context.Context, configformat.Input) (configformat.Result, error) {
	return configformat.Result{}, errors.New("the vendor document is rejected")
}

// TestRejectedDeliveryLogText pins the error a runtime built through FromConfig logs when a source's
// delivery is rejected: by flagd's format, and by a vendor format.
func TestRejectedDeliveryLogText(t *testing.T) {
	dir := t.TempDir()
	flagdSource := filepath.Join(dir, "flagd.json")
	require.NoError(t, os.WriteFile(flagdSource, []byte(`{"flags":{"f1":{"state":"ENABLED","variants":{},"defaultVariant":"on"}}}`), 0o600))
	vendorSource := filepath.Join(dir, "vendor.json")
	require.NoError(t, os.WriteFile(vendorSource, []byte(`{"anything":"goes"}`), 0o600))

	cfg := characterizedConfig(t, newPorts(t))
	cfg.SyncProviders = []coresync.SourceConfig{
		{URI: flagdSource, Provider: "file"},
		{URI: vendorSource, Provider: "file", Format: rejectingType},
	}
	cfg.ConfigurationFormats = append(cfg.ConfigurationFormats, rejectingFactory{})

	core, logs := observer.New(zap.DebugLevel)
	rt, err := FromConfig(logger.NewLogger(zap.New(core), false), "test", cfg, officialset.Components())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = rt.start(ctx) }()

	require.Eventually(t, func() bool {
		return logs.FilterMessageSnippet("error setting state").Len() >= 2
	}, 20*time.Second, 50*time.Millisecond)

	var messages []string
	for _, e := range logs.FilterMessageSnippet("error setting state").All() {
		require.Equal(t, zap.ErrorLevel, e.Level)
		messages = append(messages, e.Message)
	}
	require.ElementsMatch(t, []string{
		"error setting state: default variant: 'on' isn't a valid variant of flag: 'f1'",
		"error setting state: the vendor document is rejected",
	}, messages)
}
