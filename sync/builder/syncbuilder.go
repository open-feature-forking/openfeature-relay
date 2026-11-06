package builder

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/polling"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

const (
	providerFile       = "file"
	providerKubernetes = "kubernetes"
	providerGRPC       = "grpc"
)

type ISyncBuilder interface {
	SyncFromURI(uri string, logger *logger.Logger) (sync.ISync, error)
	SyncsFromConfig(sourceConfig []sync.SourceConfig, logger *logger.Logger) ([]sync.ISync, error)
}

// SyncBuilder creates the sync providers of sources from the factories of the sync components it knows.
type SyncBuilder struct {
	factories []sync.Factory
	// k8sClientBuilder is set by tests, with a mock of the Kubernetes client builder; the builder imports no Kubernetes
	// package itself.
	k8sClientBuilder any

	// SyncMetricsRecorder, when non-nil, is passed to every sync provider so they can
	// record the universal client-side sync metrics
	SyncMetricsRecorder sync.MetricsRecorder

	// Telemetry is passed to every sync component as Settings.Telemetry.
	Telemetry telemetry.Provider
}

// NewSyncBuilder creates a builder for the given factories. It imports no sync module: a distribution passes the
// factories of its components, such as those of the official package.
func NewSyncBuilder(factories ...sync.Factory) *SyncBuilder {
	sb := &SyncBuilder{factories: factories}
	if len(factories) == 0 && defaultFactories != nil {
		sb.factories = defaultFactories(sb)
	}
	return sb
}

// defaultFactories supplies the factories of a builder created without any. It is set by tests only.
var defaultFactories func(*SyncBuilder) []sync.Factory

// SyncFromURI creates the sync provider of a file or FeatureFlag source given as a --uri; the configuration proxy
// uses it.
func (sb *SyncBuilder) SyncFromURI(uri string, logger *logger.Logger) (sync.ISync, error) {
	for _, provider := range []string{providerFile, providerKubernetes} {
		f, ok := sb.schemeFactory(provider)
		if !ok {
			continue
		}
		if source, claimed := f.ParseURI(uri); claimed {
			return sb.syncFromConfig(source, logger)
		}
	}
	return nil, fmt.Errorf("unrecognized URI: %s", uri)
}

func (sb *SyncBuilder) SyncsFromConfig(sourceConfigs []sync.SourceConfig, logger *logger.Logger) ([]sync.ISync, error) {
	syncImpls := make([]sync.ISync, len(sourceConfigs))
	for i, syncProvider := range sourceConfigs {
		syncImpl, err := sb.syncFromConfig(syncProvider, logger)
		if err != nil {
			for _, built := range syncImpls[:i] {
				if closer, ok := built.(io.Closer); ok {
					_ = closer.Close()
				}
			}
			return nil, fmt.Errorf("could not create sync provider: %w", err)
		}
		syncImpls[i] = syncImpl
	}
	return syncImpls, nil
}

func (sb *SyncBuilder) syncFromConfig(sourceConfig sync.SourceConfig, log *logger.Logger) (sync.ISync, error) {
	if sourceConfig.GRPC != nil && sourceConfig.Provider != providerGRPC {
		return nil, fmt.Errorf("source %q: the grpc object is for sources of the provider %q, not %q", sourceConfig.URI, providerGRPC, sourceConfig.Provider)
	}
	resolution, err := sb.Resolve(sourceConfig, log)
	if err != nil {
		return nil, err
	}
	if resolution.Streaming != nil {
		log.Debug(fmt.Sprintf("using %s sync-provider for: %s", sourceConfig.Provider, sourceConfig.URI))
		return resolution.Streaming, nil
	}

	triggers := make([]sync.Trigger, len(resolution.Triggers))
	for i, config := range resolution.Triggers {
		if triggers[i], err = sb.createTrigger(config, resolution, log); err != nil {
			return nil, err
		}
	}
	log.Debug(fmt.Sprintf("using %s sync-provider for: %s", sourceConfig.Provider, sourceConfig.URI))
	return fetching.New(fetching.Options{
		Fetcher:            resolution.Fetcher,
		Triggers:           triggers,
		Logger:             log,
		Source:             resolution.Source.URI,
		SourceType:         sourceType(resolution.Factory),
		Metrics:            sb.SyncMetricsRecorder,
		Interval:           time.Duration(resolution.Interval) * time.Second,
		Timeout:            time.Duration(sourceConfig.TimeoutS) * time.Second,
		IncrementalUpdates: sourceConfig.IncrementalUpdates,
	}), nil
}

// createTrigger creates the fetch trigger a source names: the core's poller, or the trigger of a factory.
func (sb *SyncBuilder) createTrigger(config sync.TriggerConfig, resolution Resolution, log *logger.Logger) (sync.Trigger, error) {
	if config.Type == polling.TriggerType {
		poller, err := polling.NewIntervalTrigger(resolution.Interval, resolution.Source.IntervalSeed)
		if err != nil {
			return nil, fmt.Errorf("invalid %s sync configuration: %w", resolution.Source.Provider, err)
		}
		return poller, nil
	}
	for _, f := range sb.factories {
		if tf, ok := f.(sync.TriggerFactory); ok && tf.Type() == config.Type {
			return tf.CreateTrigger(sync.Settings{Source: resolution.Source, Logger: log, Config: config.Config})
		}
	}
	return nil, sb.checkTriggerType(config.Type)
}

// checkTriggerType fails for a fetch trigger type no factory provides, naming the ones available.
func (sb *SyncBuilder) checkTriggerType(triggerType string) error {
	for _, t := range sb.triggerTypes() {
		if t == triggerType {
			return nil
		}
	}
	return fmt.Errorf("unknown fetch trigger type %q, available: %s", triggerType, strings.Join(sb.triggerTypes(), ", "))
}

func (sb *SyncBuilder) triggerTypes() []string {
	types := []string{polling.TriggerType}
	for _, f := range sb.factories {
		if tf, ok := f.(sync.TriggerFactory); ok {
			types = append(types, tf.Type())
		}
	}
	return types
}

func sourceType(f sync.Factory) string {
	if st, ok := f.(sync.SourceTyper); ok {
		return st.SourceType()
	}
	return f.Type()
}

// schemeFactory finds the fetcher or streaming factory of a provider name.
func (sb *SyncBuilder) schemeFactory(provider string) (sync.SchemeFactory, bool) {
	for _, f := range sb.factories {
		if sf, ok := f.(sync.SchemeFactory); ok && f.Type() == provider {
			return sf, true
		}
	}
	return nil, false
}

// providerNames are the provider names the factories are selected by, in order.
func (sb *SyncBuilder) providerNames() []string {
	var names []string
	for _, f := range sb.factories {
		if _, ok := f.(sync.SchemeFactory); !ok {
			continue
		}
		names = append(names, f.Type())
		if aliased, ok := f.(sync.Aliased); ok {
			for _, alias := range aliased.Aliases() {
				names = append(names, alias.Name)
			}
		}
	}
	return names
}

func quoteAnd(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "'" + n + "'"
	}
	if len(quoted) < 2 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}
