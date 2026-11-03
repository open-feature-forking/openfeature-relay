package runtime

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

type readySync struct {
	ready bool
}

func (s readySync) Init(context.Context) error {
	return nil
}

func (s readySync) Sync(context.Context, chan<- coresync.DataSync) error {
	return nil
}

func (s readySync) ReSync(context.Context, chan<- coresync.DataSync) error {
	return nil
}

func (s readySync) IsReady() bool {
	return s.ready
}

// deliveryFormat is a configuration format that accepts every delivery as a new version of the document of its
// source, so that each delivery publishes a configuration change event. errs are the outcomes of the first
// deliveries: a nil entry accepts, an error rejects.
type deliveryFormat struct {
	errs []error
	n    int
	// accepted are the sources of the deliveries, in order, rejected ones included.
	accepted []string
}

func (*deliveryFormat) Type() string { return "delivery" }

func (f *deliveryFormat) Accept(_ context.Context, in configformat.Input) (configformat.Result, error) {
	f.n++
	f.accepted = append(f.accepted, in.Source)
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return configformat.Result{}, err
		}
	}
	return configformat.Result{Documents: []configformat.DocumentReport{{FlagSet: in.Source, Version: strconv.Itoa(f.n)}}}, nil
}

// deliveredSources subscribes to the configuration change events of the bus; the returned function returns the
// sources of the events published since its last call.
func deliveredSources(t *testing.T, bus *events.Bus) func() []string {
	t.Helper()
	sub := bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindConfigurationChange}, Buffer: 16})
	t.Cleanup(sub.Close)
	return func() []string {
		var sources []string
		for {
			select {
			case ev := <-sub.C():
				sources = append(sources, ev.(events.ConfigurationChange).Source)
			default:
				return sources
			}
		}
	}
}

type controlledSync struct {
	payloads <-chan coresync.DataSync
	started  chan<- struct{}
}

func (s controlledSync) Init(context.Context) error {
	return nil
}

func (s controlledSync) Sync(ctx context.Context, dataSync chan<- coresync.DataSync) error {
	select {
	case s.started <- struct{}{}:
	case <-ctx.Done():
		return nil
	}

	for {
		select {
		case payload := <-s.payloads:
			select {
			case dataSync <- payload:
			case <-ctx.Done():
				return nil
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (s controlledSync) ReSync(context.Context, chan<- coresync.DataSync) error {
	return nil
}

func (s controlledSync) IsReady() bool {
	return true
}

// blockingEndpoint serves until the context ends, or fails once stop is closed.
type blockingEndpoint struct {
	stop <-chan struct{}
}

func (e blockingEndpoint) Serve(ctx context.Context) error {
	select {
	case <-e.stop:
		return errors.New("test complete")
	case <-ctx.Done():
		return nil
	}
}

func requireSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()

	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for runtime signal")
	}
}

func TestRuntimeReadinessRequiresSuccessfulStateForEverySource(t *testing.T) {
	const (
		firstSource  = "file:/first.flagd.json"
		secondSource = "file:/second.flagd.json"
	)

	firstPayload := coresync.DataSync{Source: firstSource}
	secondPayload := coresync.DataSync{Source: secondSource}
	evaluatorError := errors.New("invalid flag configuration")

	bus := events.NewBus()
	delivered := deliveredSources(t, bus)
	format := &deliveryFormat{errs: []error{evaluatorError}}
	runtime := Runtime{
		DefaultFormat: format,
		Logger:        logger.NewLogger(nil, false),
		Bus:           bus,
		Syncs: []coresync.ISync{
			readySync{ready: true},
			readySync{ready: true},
		},
		sourceReadiness: map[string]bool{
			firstSource:  false,
			secondSource: false,
		},
	}

	require.False(t, runtime.isReady())

	runtime.updateAndEmit(firstPayload)
	require.False(t, runtime.isReady())

	runtime.updateAndEmit(firstPayload)
	require.False(t, runtime.isReady())

	runtime.updateAndEmit(secondPayload)
	require.True(t, runtime.isReady())
	require.Equal(t, []string{firstSource, secondSource}, delivered())
	require.Equal(t, []string{firstSource, firstSource, secondSource}, format.accepted, "the rejected delivery and its retry both reach the format")
}

func TestRuntimeStartWaitsForEverySource(t *testing.T) {
	const (
		firstSource  = "file:/first.flagd.json"
		secondSource = "file:/second.flagd.json"
	)

	firstPayload := coresync.DataSync{Source: firstSource}
	secondPayload := coresync.DataSync{Source: secondSource}
	started := make(chan struct{}, 2)
	firstProviderData := make(chan coresync.DataSync)
	secondProviderData := make(chan coresync.DataSync)
	stop := make(chan struct{})

	bus := events.NewBus()
	sub := bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindConfigurationChange}, Buffer: 2})
	defer sub.Close()
	emitted := make(chan struct{}, 2)
	go func() {
		for range sub.C() {
			emitted <- struct{}{}
		}
	}()

	format := &deliveryFormat{}
	runtime := Runtime{
		DefaultFormat: format,
		Logger:        logger.NewLogger(nil, false),
		Bus:           bus,
		Endpoints:     []NamedEndpoint{{Type: "blocking", Endpoint: blockingEndpoint{stop: stop}}},
		Syncs: []coresync.ISync{
			controlledSync{payloads: firstProviderData, started: started},
			controlledSync{payloads: secondProviderData, started: started},
		},
		sourceReadiness: map[string]bool{
			firstSource:  false,
			secondSource: false,
		},
	}

	startErr := make(chan error, 1)
	go func() {
		startErr <- runtime.Start()
	}()

	requireSignal(t, started)
	requireSignal(t, started)

	// The event is published while the runtime holds its lock, so taking the lock after it waits for the readiness
	// bookkeeping of the delivery.
	firstProviderData <- firstPayload
	requireSignal(t, emitted)
	runtime.mu.Lock()
	runtime.mu.Unlock() //nolint:staticcheck // SA2001 waits for the delivery to finish
	require.False(t, runtime.isReady())

	secondProviderData <- secondPayload
	requireSignal(t, emitted)
	runtime.mu.Lock()
	runtime.mu.Unlock() //nolint:staticcheck // SA2001 waits for the delivery to finish
	require.True(t, runtime.isReady())

	require.Equal(t, []string{firstSource, secondSource}, format.accepted)
	close(stop)
	require.Error(t, <-startErr)
}

// contactSync is a sync provider that reports when it last reached its source.
type contactSync struct {
	readySync
	last time.Time
}

func (s *contactSync) LastContact() time.Time { return s.last }

func TestReadinessLostWhenStale(t *testing.T) {
	const source = "file:/flags.json"
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	now := at
	provider := &contactSync{readySync: readySync{ready: true}, last: at}
	runtime := Runtime{
		Logger:      logger.NewLogger(nil, false),
		Syncs:       []coresync.ISync{provider},
		staleLimits: []staleLimit{{source: source, provider: provider, after: time.Minute}},
		now:         func() time.Time { return now },
	}
	runtime.initialized.Store(true)

	require.True(t, runtime.isReady())
	now = at.Add(time.Minute)
	require.True(t, runtime.isReady(), "exactly staleAfter is not past it")
	now = at.Add(time.Minute + time.Second)
	require.False(t, runtime.isReady(), "flagd is not ready once the source is past staleAfter")

	t.Run("readiness is back after a contact", func(t *testing.T) {
		provider.last = now
		require.True(t, runtime.isReady())
	})

	t.Run("a source that was never reached is stale", func(t *testing.T) {
		provider.last = time.Time{}
		require.False(t, runtime.isReady())
	})
}

func TestReadinessWithoutStaleAfterIgnoresContact(t *testing.T) {
	provider := &contactSync{readySync: readySync{ready: true}, last: time.Unix(1, 0)}
	runtime := Runtime{Logger: logger.NewLogger(nil, false), Syncs: []coresync.ISync{provider}}
	runtime.initialized.Store(true)

	require.True(t, runtime.isReady(), "a source never goes stale without the setting")
}

func TestStaleLimitsNeedAProviderThatReportsContact(t *testing.T) {
	sources := []coresync.SourceConfig{{URI: "a", Provider: "file"}, {URI: "b", Provider: "kubernetes", StaleAfter: 30}}

	_, err := staleLimitsOf(sources, []coresync.ISync{&contactSync{}, readySync{}})
	require.EqualError(t, err, `source "b" sets staleAfter, which the kubernetes sync provider cannot honour: it does not report its last contact`)

	sources[1].Provider = "grpc"
	limits, err := staleLimitsOf(sources, []coresync.ISync{readySync{}, &contactSync{}})
	require.NoError(t, err)
	require.Len(t, limits, 1)
	require.Equal(t, "b", limits[0].source)
	require.Equal(t, 30*time.Second, limits[0].after)
}
