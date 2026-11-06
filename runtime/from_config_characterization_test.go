package runtime

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
	gosync "sync"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest/observer"
)

// callLog records, in order, what the fakes of a build were asked to do.
type callLog struct {
	mu    gosync.Mutex
	calls []string
}

func (l *callLog) add(call string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call)
}

func (l *callLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

var errBoom = errors.New("boom")

type recTelemetry struct {
	name string
	log  *callLog
	err  error
}

func (f recTelemetry) Type() string { return f.name }

func (f recTelemetry) Create(context.Context, telemetry.Settings) (telemetry.Provider, error) {
	f.log.add("telemetry create")
	if f.err != nil {
		return nil, f.err
	}
	return recProvider{Provider: telemetry.Noop(), log: f.log}, nil
}

type recProvider struct {
	telemetry.Provider
	log *callLog
}

func (p recProvider) Shutdown(context.Context) error {
	p.log.add("telemetry shutdown")
	return nil
}

type recFormatFactory struct {
	name string
	log  *callLog
	err  error
}

func (f recFormatFactory) Type() string { return f.name }

func (f recFormatFactory) Create(s configformat.Settings) (configformat.ConfigurationFormat, error) {
	f.log.add("format " + f.name + " create")
	s.Logger.Info("format " + f.name)
	if f.err != nil {
		return nil, f.err
	}
	return recFormat{name: f.name}, nil
}

type recFormat struct{ name string }

func (f recFormat) Type() string { return f.name }

func (recFormat) Accept(context.Context, configformat.Input) (configformat.Result, error) {
	return configformat.Result{}, nil
}

type recEvaluatorFactory struct {
	name, format string
	log          *callLog
	err          error
}

func (f recEvaluatorFactory) Type() string                { return f.name }
func (f recEvaluatorFactory) ConfigurationFormat() string { return f.format }

func (f recEvaluatorFactory) Create(s evaluator.Settings) (evaluator.Evaluator, error) {
	f.log.add("evaluator " + f.name + " create")
	s.Logger.Info("evaluator " + f.name)
	if f.err != nil {
		return nil, f.err
	}
	return recEvaluator{name: f.name, log: f.log}, nil
}

type recEvaluator struct {
	name string
	log  *callLog
}

func (recEvaluator) Evaluate(_ context.Context, _ evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	return dst, nil, nil
}

func (e recEvaluator) Close() error {
	e.log.add("evaluator " + e.name + " close")
	return nil
}

type recMiddlewareFactory struct {
	name string
	log  *callLog
	err  error
}

func (f recMiddlewareFactory) Type() string { return f.name }

func (f recMiddlewareFactory) Create(s middleware.Settings) (middleware.Middleware, error) {
	f.log.add("middleware " + f.name + " create")
	s.Logger.Info("middleware " + f.name)
	if f.err != nil {
		return nil, f.err
	}
	return recMiddleware{}, nil
}

type recMiddleware struct{}

func (recMiddleware) Handler(next http.Handler) http.Handler { return next }

type recEndpointFactory struct {
	name     string
	needs    service.Needs
	log      *callLog
	err      error
	settings *[]service.Settings
}

func (f recEndpointFactory) Type() string         { return f.name }
func (f recEndpointFactory) Needs() service.Needs { return f.needs }

func (f recEndpointFactory) Create(s service.Settings) (service.Endpoint, error) {
	f.log.add("endpoint " + f.name + " create")
	if f.settings != nil {
		*f.settings = append(*f.settings, s)
	}
	if f.err != nil {
		return nil, f.err
	}
	return recEndpoint{name: f.name, log: f.log}, nil
}

type recEndpoint struct {
	name string
	log  *callLog
}

func (recEndpoint) Serve(ctx context.Context) error { <-ctx.Done(); return nil }

func (e recEndpoint) Close() error {
	e.log.add("endpoint " + e.name + " close")
	return nil
}

type recSyncFactory struct {
	log *callLog
	err error
	// failOn is the URI whose create fails; empty fails every one.
	failOn string
}

func (recSyncFactory) Type() string      { return "rec" }
func (recSyncFactory) Schemes() []string { return []string{"rec://"} }

func (recSyncFactory) ParseURI(uri string) (coresync.SourceConfig, bool) {
	return coresync.SourceConfig{URI: uri, Provider: "rec"}, strings.HasPrefix(uri, "rec://")
}

func (f recSyncFactory) CreateStreaming(s coresync.Settings) (coresync.StreamingSyncProvider, error) {
	f.log.add("sync " + s.Source.URI + " create")
	s.Logger.Info("sync " + s.Source.URI)
	if f.err != nil && (f.failOn == "" || f.failOn == s.Source.URI) {
		return nil, f.err
	}
	return recSync{uri: s.Source.URI, log: f.log}, nil
}

type recSync struct {
	uri string
	log *callLog
}

func (recSync) Init(context.Context) error                                 { return nil }
func (recSync) Sync(ctx context.Context, _ chan<- coresync.DataSync) error { <-ctx.Done(); return nil }
func (recSync) ReSync(context.Context, chan<- coresync.DataSync) error     { return nil }
func (recSync) IsReady() bool                                              { return true }

func (s recSync) Close() error {
	s.log.add("sync " + s.uri + " close")
	return nil
}

type recSinkFactory struct {
	name string
	log  *callLog
	err  error
}

func (f recSinkFactory) Type() string       { return f.name }
func (recSinkFactory) Kinds() []events.Kind { return []events.Kind{events.KindEvaluation} }

func (f recSinkFactory) Create(eventsink.Settings) (eventsink.Sink, error) {
	f.log.add("sink " + f.name + " create")
	if f.err != nil {
		return nil, f.err
	}
	return recSink{name: f.name, log: f.log}, nil
}

type recSink struct {
	name string
	log  *callLog
}

func (recSink) Deliver(context.Context, []events.Event) error { return nil }

func (s recSink) Close() error {
	s.log.add("sink " + s.name + " close")
	return nil
}

// recBuild is a build whose every component is a fake that records into log.
type recBuild struct {
	log        *callLog
	config     Config
	components Components
	settings   []service.Settings
}

func newRecBuild() *recBuild {
	b := &recBuild{log: &callLog{}}
	b.config = Config{
		KeepAliveMinTime: KeepAliveMinTimeDefault, KeepAlivePermitWithoutStream: KeepAlivePermitWithoutStreamDefault,
		SyncProviders: []coresync.SourceConfig{
			{URI: "rec://a", Provider: "rec"},
			{URI: "rec://b", Provider: "rec", Format: "other"},
		},
	}
	b.components = Components{
		Telemetry: recTelemetry{name: "rec-telemetry", log: b.log},
		ConfigurationFormats: []configformat.Factory{
			recFormatFactory{name: configformat.DefaultType, log: b.log},
			recFormatFactory{name: "other", log: b.log},
		},
		Evaluators: []evaluator.Factory{recEvaluatorFactory{name: "ev", format: configformat.DefaultType, log: b.log}},
		Middleware: []middleware.Configured{{Factory: recMiddlewareFactory{name: "mw", log: b.log}}},
		Endpoints: []service.Factory{
			recEndpointFactory{name: "e1", needs: service.Needs{Router: true}, log: b.log, settings: &b.settings},
			recEndpointFactory{name: "e2", log: b.log, settings: &b.settings},
		},
		Sync:           []coresync.Factory{recSyncFactory{log: b.log}},
		EventSinks:     []eventsink.Configured{{Factory: recSinkFactory{name: "sink", log: b.log}}},
		MetricsHandler: http.NotFoundHandler(),
	}
	return b
}

func (b *recBuild) run(t *testing.T) (*Runtime, error) {
	t.Helper()
	return b.runWith(t, logger.NewLogger(nil, false))
}

func (b *recBuild) runWith(t *testing.T, log *logger.Logger) (*Runtime, error) {
	t.Helper()
	rt, err := FromConfig(log, "v0.0.0-test", b.config, b.components)
	if err == nil {
		require.NotNil(t, rt)
	} else {
		require.Nil(t, rt, "a failed build returns no runtime")
	}
	return rt, err
}

// TestFromConfigErrorTexts pins the error of each step that can fail, exactly. store.NewStore fails only on an
// invalid schema, which no configuration reaches, so "error creating flag store" has no row.
func TestFromConfigErrorTexts(t *testing.T) {
	for name, tt := range map[string]struct {
		change func(b *recBuild)
		want   string
	}{
		"telemetry declares": {
			func(b *recBuild) { b.components.Telemetry = recTelemetry{log: b.log} },
			"the telemetry provider declares: the type name is empty",
		},
		"telemetry create": {
			func(b *recBuild) {
				b.components.Telemetry = recTelemetry{name: "rec-telemetry", log: b.log, err: errBoom}
			},
			`error creating the telemetry provider "rec-telemetry": boom`,
		},
		"event sinks need metrics": {
			func(b *recBuild) {
				b.config.EventSinksNeedMetrics = true
				b.components.MetricsHandler = nil
			},
			"the setting event-sinks needs a telemetry provider that serves /metrics, because the events an event sink " +
				"does not get are reported on the management port; the telemetry provider of this distribution " +
				"(rec-telemetry) serves none",
		},
		"format of a source": {
			func(b *recBuild) {
				b.components.ConfigurationFormats[1] = recFormatFactory{name: "other", log: b.log, err: errBoom}
			},
			`error creating configuration format "other" for source "rec://b": boom`,
		},
		"default format": {
			func(b *recBuild) {
				b.components.ConfigurationFormats[0] = recFormatFactory{name: configformat.DefaultType, log: b.log, err: errBoom}
			},
			`error creating configuration format "flagd": boom`,
		},
		"no default format": {
			func(b *recBuild) { b.components.ConfigurationFormats = b.components.ConfigurationFormats[1:] },
			`source "rec://a" names no configuration format, and this distribution has no "flagd" format; available formats: other`,
		},
		"evaluator of a missing format": {
			func(b *recBuild) {
				// other before flagd: the list in the text is sorted
				b.components.ConfigurationFormats[0], b.components.ConfigurationFormats[1] =
					b.components.ConfigurationFormats[1], b.components.ConfigurationFormats[0]
				b.components.Evaluators = []evaluator.Factory{recEvaluatorFactory{name: "ev", format: "nope", log: b.log}}
			},
			`the evaluator "ev" resolves the configuration format "nope", which the distribution lacks; it has: flagd, other`,
		},
		"evaluator create": {
			func(b *recBuild) {
				b.components.Evaluators = []evaluator.Factory{
					recEvaluatorFactory{name: "ev", format: configformat.DefaultType, log: b.log, err: errBoom},
				}
			},
			`error creating the evaluator "ev": boom`,
		},
		"no evaluator": {
			func(b *recBuild) { b.components.Evaluators = nil },
			"the distribution has no evaluator, but a service endpoint needs the evaluation router; its configuration " +
				"formats are: flagd, other",
		},
		"endpoint needs a missing evaluator": {
			func(b *recBuild) {
				b.components.Endpoints[1] = recEndpointFactory{name: "e2", needs: service.Needs{Format: "other"}, log: b.log}
			},
			`the service endpoint "e2" needs the evaluator of the configuration format "other", which the distribution lacks`,
		},
		"middleware declares": {
			func(b *recBuild) {
				b.components.Middleware = []middleware.Configured{{Factory: recMiddlewareFactory{log: b.log}}}
			},
			"the middleware factory at position 0 declares: the type name is empty",
		},
		"middleware create": {
			func(b *recBuild) {
				b.components.Middleware = []middleware.Configured{{Factory: recMiddlewareFactory{name: "mw", log: b.log, err: errBoom}}}
			},
			`error creating the middleware "mw": boom`,
		},
		"middleware setting": {
			func(b *recBuild) {
				b.components.Middleware = []middleware.Configured{{
					Factory: recMiddlewareFactory{name: "mw", log: b.log},
					Config:  map[string]any{"key": map[string]any{"env": "RELAY_TEST_UNSET_VARIABLE"}},
				}}
			},
			`error creating the middleware "mw": setting "key": environment variable "RELAY_TEST_UNSET_VARIABLE" is not set`,
		},
		"endpoint declares": {
			func(b *recBuild) { b.components.Endpoints[1] = recEndpointFactory{log: b.log} },
			`the service endpoint "" declares: the type name is empty`,
		},
		"endpoint create": {
			func(b *recBuild) {
				b.components.Endpoints[1] = recEndpointFactory{name: "e2", log: b.log, err: errBoom}
			},
			`error creating the service endpoint "e2": boom`,
		},
		"sync create": {
			func(b *recBuild) { b.components.Sync = []coresync.Factory{recSyncFactory{log: b.log, err: errBoom}} },
			"could not create sync sources from config: could not create sync provider: boom",
		},
		"unknown sync provider": {
			func(b *recBuild) { b.config.SyncProviders[0].Provider = "nope" },
			`could not create sync sources from config: could not create sync provider: invalid sync provider: nope, must be one of with 'rec'`,
		},
		"staleAfter": {
			func(b *recBuild) { b.config.SyncProviders[0].StaleAfter = 5 },
			`source "rec://a" sets staleAfter, which the rec sync provider cannot honour: it does not report its last contact`,
		},
		"event sink declares": {
			func(b *recBuild) {
				b.components.EventSinks = []eventsink.Configured{{Factory: recSinkFactory{log: b.log}}}
			},
			"the event sink factory at position 0 declares: the type name is empty",
		},
		"event sink create": {
			func(b *recBuild) {
				b.components.EventSinks = []eventsink.Configured{{Factory: recSinkFactory{name: "sink", log: b.log, err: errBoom}}}
			},
			`event sink "sink": boom`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := newRecBuild()
			tt.change(b)
			_, err := b.run(t)
			require.EqualError(t, err, tt.want)
		})
	}
}

// TestFromConfigCallOrder pins the order in which a build creates its components, and what a failed build releases
// and what it does not.
func TestFromConfigCallOrder(t *testing.T) {
	built := []string{
		"telemetry create",
		"format other create", "format flagd create",
		"evaluator ev create",
		"middleware mw create",
		"endpoint e1 create", "endpoint e2 create",
		"sync rec://a create", "sync rec://b create",
		"sink sink create",
	}

	t.Run("a valid build", func(t *testing.T) {
		b := newRecBuild()
		rt, err := b.run(t)
		require.NoError(t, err)
		require.Equal(t, built, b.log.all())
		require.Len(t, rt.Formats, 1, "flagd's own format is the default, not an entry")
		require.Contains(t, rt.Formats, "rec://b", "the formats are keyed by source URI")
		var hooks []string
		for _, h := range rt.hooks {
			hooks = append(hooks, h.name)
		}
		require.Equal(t, []string{"event sinks", "evaluator", "telemetry"}, hooks)
	})

	t.Run("no endpoint needs the router", func(t *testing.T) {
		b := newRecBuild()
		b.components.Endpoints = b.components.Endpoints[1:]
		_, err := b.run(t)
		require.NoError(t, err)
		require.NotContains(t, b.log.all(), "evaluator ev create", "no evaluator is created")
	})

	for name, tt := range map[string]struct {
		change func(b *recBuild)
		want   []string
	}{
		"middleware fails: the evaluator closes": {
			func(b *recBuild) {
				b.components.Middleware = []middleware.Configured{{Factory: recMiddlewareFactory{name: "mw", log: b.log, err: errBoom}}}
			},
			[]string{
				"telemetry create", "format other create", "format flagd create", "evaluator ev create",
				"middleware mw create", "evaluator ev close", "telemetry shutdown",
			},
		},
		"staleAfter fails: the sync providers close, then the endpoints and the evaluator": {
			func(b *recBuild) { b.config.SyncProviders[1].StaleAfter = 5 },
			append(append([]string(nil), built[:9]...),
				"sync rec://a close", "sync rec://b close", "endpoint e1 close", "endpoint e2 close", "evaluator ev close",
				"telemetry shutdown"),
		},
		"an event sink fails: the earlier one closes, then the syncs, the endpoints and the evaluator": {
			func(b *recBuild) {
				b.components.EventSinks = append(b.components.EventSinks,
					eventsink.Configured{Factory: recSinkFactory{name: "sink2", log: b.log, err: errBoom}})
			},
			append(append([]string(nil), built...),
				"sink sink2 create", "sink sink close", "sync rec://a close", "sync rec://b close",
				"endpoint e1 close", "endpoint e2 close", "evaluator ev close", "telemetry shutdown"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := newRecBuild()
			tt.change(b)
			_, err := b.run(t)
			require.Error(t, err)
			require.Equal(t, tt.want, b.log.all())
		})
	}
}

// TestFailedFromConfigCleanupOrder pins what a failed build releases, in which order: the endpoints it created
// close before the telemetry provider shuts down, and a failure before any endpoint or in the telemetry provider
// itself releases less.
func TestFailedFromConfigCleanupOrder(t *testing.T) {
	for name, tt := range map[string]struct {
		change func(b *recBuild)
		want   []string
	}{
		"after an endpoint: the endpoint and the evaluator close, then telemetry shuts down": {
			func(b *recBuild) {
				b.components.Endpoints[1] = recEndpointFactory{name: "e2", log: b.log, err: errBoom}
			},
			[]string{
				"telemetry create", "format other create", "format flagd create", "evaluator ev create",
				"middleware mw create", "endpoint e1 create", "endpoint e2 create", "endpoint e1 close", "evaluator ev close", "telemetry shutdown",
			},
		},
		"before any endpoint: telemetry shuts down, nothing closes": {
			func(b *recBuild) {
				b.components.ConfigurationFormats[1] = recFormatFactory{name: "other", log: b.log, err: errBoom}
			},
			[]string{"telemetry create", "format other create", "telemetry shutdown"},
		},
		"in the telemetry provider: nothing is released": {
			func(b *recBuild) { b.components.Telemetry = recTelemetry{name: "t", log: b.log, err: errBoom} },
			[]string{"telemetry create"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := newRecBuild()
			tt.change(b)
			_, err := b.run(t)
			require.Error(t, err)
			require.Equal(t, tt.want, b.log.all())
		})
	}
}

// TestFromConfigFirstFailureWins pins that with two faults the earlier step's error is the one returned.
func TestFromConfigFirstFailureWins(t *testing.T) {
	t.Run("bad middleware and a failing endpoint", func(t *testing.T) {
		b := newRecBuild()
		b.components.Middleware = []middleware.Configured{{Factory: recMiddlewareFactory{name: "mw", log: b.log, err: errBoom}}}
		b.components.Endpoints[1] = recEndpointFactory{name: "e2", log: b.log, err: errors.New("endpoint")}
		_, err := b.run(t)
		require.EqualError(t, err, `error creating the middleware "mw": boom`)
	})
	t.Run("no default format and a bad sync provider", func(t *testing.T) {
		b := newRecBuild()
		b.components.ConfigurationFormats = b.components.ConfigurationFormats[1:]
		b.config.SyncProviders[1].Provider = "nope"
		_, err := b.run(t)
		require.EqualError(t, err,
			`source "rec://a" names no configuration format, and this distribution has no "flagd" format; available formats: other`)
	})
}

// TestFromConfigLogLines pins the log lines of a build and the component fields of the loggers each component gets.
func TestFromConfigLogLines(t *testing.T) {
	b := newRecBuild()
	b.config.KeepAliveMinTime = 0
	b.config.SyncProviders = append(b.config.SyncProviders, coresync.SourceConfig{URI: "rec://c", Provider: "rec", Format: "missing"})
	log, logs := observedLogger()
	rt, err := b.runWith(t, log)
	require.NoError(t, err)

	message := func(text string) observer.LoggedEntry {
		t.Helper()
		found := logs.FilterMessage(text).All()
		require.Len(t, found, 1, text)
		return found[0]
	}
	fields := func(e observer.LoggedEntry) map[string]any { return e.ContextMap() }

	require.Equal(t, "warn", message("keep-alive-min-time and keep-alive-permit-without-stream are no-ops and will be "+
		"removed in a later version. The flag sync server is served by connect over net/http, which imposes no minimum "+
		"keepalive ping interval and always permits pings without an active stream.").Level.String())
	skipped := message(`skipping source "rec://c": configuration format "missing" is not available, available formats: flagd, other`)
	require.Equal(t, "error", skipped.Level.String())

	require.Equal(t, map[string]any{"component": "configformat"}, fields(message("format flagd")),
		"the default format gets the configformat logger")
	require.Empty(t, fields(message("format other")), "the format of a source gets the runtime's logger as it is")
	require.Empty(t, fields(message("evaluator ev")))
	require.Equal(t, map[string]any{"component": "middleware", "middleware": "mw"}, fields(message("middleware mw")))
	require.Equal(t, map[string]any{"component": "sync"}, fields(message("sync rec://a")))

	rt.Logger.Info("runtime line")
	require.Equal(t, map[string]any{"component": "runtime"}, fields(message("runtime line")))
	for _, s := range b.settings {
		require.Same(t, log, s.Logger, "an endpoint gets the runtime's logger as it is")
	}
}

// TestEndpointSettingsAreNotShared pins the settings each endpoint gets: a Formats slice of its own and the values
// of the build.
func TestEndpointSettingsAreNotShared(t *testing.T) {
	b := newRecBuild()
	var listened []string
	b.components.Listen = func(network, address string) (net.Listener, error) {
		listened = append(listened, address)
		return nil, errBoom
	}
	rt, err := b.run(t)
	require.NoError(t, err)
	require.Len(t, b.settings, 2)
	first, second := b.settings[0], b.settings[1]

	require.Equal(t, []string{"flagd", "flagd", "other"}, first.Formats, "flagd's own type, then every format of the distribution")
	require.Equal(t, first.Formats, second.Formats)
	first.Formats[0] = "changed"
	require.Equal(t, "flagd", second.Formats[0], "each endpoint has its own slice")

	for _, s := range b.settings {
		require.Equal(t, []string{"rec://a", "rec://b"}, s.Sources)
		require.Equal(t, rt.Telemetry, s.Telemetry)
		require.NotNil(t, s.HTTPMiddleware)
		require.Equal(t, rt.Initialized(), s.Initialized)
		require.Equal(t, reflect.ValueOf(b.components.Listen).Pointer(), reflect.ValueOf(s.Listen).Pointer())
	}
	require.NotNil(t, first.Resolver, "e1 needs the router")
	require.Nil(t, second.Resolver)
	require.Empty(t, listened, "a build opens no listener")
}

// TestFromConfigReleasesPartialBuilds pins that a step that fails halfway closes what it had built itself, and that the
// build then closes the rest.
func TestFromConfigReleasesPartialBuilds(t *testing.T) {
	for name, tt := range map[string]struct {
		change func(b *recBuild)
		want   []string
	}{
		"the second evaluator fails: the first closes": {
			func(b *recBuild) {
				b.components.Evaluators = []evaluator.Factory{
					recEvaluatorFactory{name: "ev", format: configformat.DefaultType, log: b.log},
					recEvaluatorFactory{name: "ev2", format: "other", log: b.log, err: errBoom},
				}
			},
			[]string{
				"telemetry create", "format other create", "format flagd create", "evaluator ev create",
				"evaluator ev2 create", "evaluator ev close", "telemetry shutdown",
			},
		},
		"the second evaluator lacks its format: the first closes": {
			func(b *recBuild) {
				b.components.Evaluators = []evaluator.Factory{
					recEvaluatorFactory{name: "ev", format: configformat.DefaultType, log: b.log},
					recEvaluatorFactory{name: "ev2", format: "nope", log: b.log},
				}
			},
			[]string{
				"telemetry create", "format other create", "format flagd create", "evaluator ev create",
				"evaluator ev close", "telemetry shutdown",
			},
		},
		"an endpoint needs a missing format: the evaluator closes": {
			func(b *recBuild) {
				b.components.Endpoints[1] = recEndpointFactory{name: "e2", needs: service.Needs{Format: "other"}, log: b.log}
			},
			[]string{
				"telemetry create", "format other create", "format flagd create", "evaluator ev create",
				"evaluator ev close", "telemetry shutdown",
			},
		},
		"the second sync fails: the first closes, then the endpoints and the evaluator": {
			func(b *recBuild) {
				b.components.Sync = []coresync.Factory{recSyncFactory{log: b.log, err: errBoom, failOn: "rec://b"}}
			},
			[]string{
				"telemetry create", "format other create", "format flagd create", "evaluator ev create",
				"middleware mw create", "endpoint e1 create", "endpoint e2 create", "sync rec://a create",
				"sync rec://b create", "sync rec://a close", "endpoint e1 close", "endpoint e2 close",
				"evaluator ev close", "telemetry shutdown",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := newRecBuild()
			tt.change(b)
			_, err := b.run(t)
			require.Error(t, err)
			require.Equal(t, tt.want, b.log.all())
		})
	}
}

// TestRunClosesTheEvaluatorOnShutdown pins that a built runtime closes its evaluator once when it stops, after the
// event sinks flush and before telemetry shuts down.
func TestRunClosesTheEvaluatorOnShutdown(t *testing.T) {
	b := newRecBuild()
	b.components.Listen = func(string, string) (net.Listener, error) { return nil, errBoom }
	rt, err := b.run(t)
	require.NoError(t, err)

	require.Error(t, rt.start(context.Background()), "the management port fails to open")
	calls := b.log.all()
	require.Equal(t, []string{"evaluator ev close", "telemetry shutdown"}, calls[len(calls)-2:])
	n := 0
	for _, c := range calls {
		if c == "evaluator ev close" {
			n++
		}
	}
	require.Equal(t, 1, n, "closed once")
}
