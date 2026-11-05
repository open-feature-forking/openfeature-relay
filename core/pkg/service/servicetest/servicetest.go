// Package servicetest is the contract every service endpoint has to pass. An endpoint's tests call Run with
// its factory and Inputs, which connect the harness to the endpoint's protocol; each failed case prints the
// expected and the observed behaviour.
//
// A case applies according to what the factory declares (its Needs) and to the inputs the author supplies for
// the features the endpoint has: documents, a cache validator, announcements, relayed events. No case can be
// skipped: an applicable case whose input is missing fails, naming the input.
package servicetest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/contracttest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/typed"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

const (
	// Budget is how long an endpoint has to return from Serve and end its clients' connections once the
	// context ends.
	Budget = 5 * time.Second

	source     = "contract-source"
	otherSet   = "other-set"
	flagSet    = "contract-set"
	flagKey    = "contract-flag"
	missingKey = "contract-missing"
	formatName = "contract-format"
)

// The documents and flags the harness stores, for the inputs that need to name them.
const (
	Source         = source
	FlagSet        = flagSet
	FlagKey        = flagKey
	MissingFlagKey = missingKey
	VendorFormat   = formatName
)

// Answer is what an endpoint's client learns from one resolution.
type Answer struct {
	// ErrorCode is the OpenFeature error code the client was given, or empty.
	ErrorCode string
}

// Batch is what an in-process provider sends an endpoint that collects events.
type Batch struct {
	Evaluations []events.Evaluation
	Trackings   []events.Tracking
}

// Feature is something an endpoint may have, which the contract checks through an input. The names are those
// of the inputs.
type Feature string

const (
	// FeatureDocuments is serving flag configurations: the input Documents.
	FeatureDocuments Feature = "Documents"
	// FeatureValidator is giving clients a cache validator: the input Validator.
	FeatureValidator Feature = "Validator"
	// FeatureListen is announcing changes to clients: the input Listen.
	FeatureListen Feature = "Listen"
	// FeatureSendEvents is collecting events from in-process providers: the input SendEvents.
	FeatureSendEvents Feature = "SendEvents"
	// FeatureHTTP is serving HTTP through Settings.WrapHTTP: the field HTTP. An endpoint that resolves flags
	// has to set it or name it in Without.
	FeatureHTTP Feature = "HTTP"
)

var allFeatures = []Feature{FeatureDocuments, FeatureValidator, FeatureListen, FeatureSendEvents, FeatureHTTP}

// Inputs connect the harness to the endpoint's protocol. Every function receives the address the endpoint
// listens on, "host:port" for the free port the harness put into Config under PortKey, or "" without PortKey.
type Inputs struct {
	// PortKey is the key of Config under which the harness passes the TCP port the endpoint listens on.
	PortKey string
	// Config is the endpoint's own configuration.
	Config map[string]any
	// HTTP says the endpoint serves HTTP through Settings.WrapHTTP; the harness then supplies middleware that
	// binds a selector and attributes, and checks the effective selector.
	HTTP bool
	// Without names the features the endpoint does not have. A feature is either supplied through its input or
	// named here; one that is neither fails its case, naming the input, so that leaving an input out cannot
	// pass the contract.
	Without []Feature

	// Connect opens a client connection that stays open, for example a stream, and returns a channel that is
	// closed when the endpoint ends the connection. It fails until the endpoint serves, and gives up on its own
	// after a second or so; the connection must outlive the call. Required.
	Connect func(ctx context.Context, addr string) (ended <-chan struct{}, err error)
	// Resolve resolves a flag through the protocol, with selector as the client's selector expression.
	// Required when the factory declares Needs.
	Resolve func(ctx context.Context, addr, flagKey, selector string) (Answer, error)
	// Documents returns the documents of the format the endpoint serves for the selector, as the client
	// receives them. Set by endpoints that serve flag configurations.
	Documents func(ctx context.Context, addr, format, selector string) ([]store.Document, error)
	// Validator returns the cache validator the endpoint gives a client for the selector. Set by endpoints that
	// derive one from document versions.
	Validator func(ctx context.Context, addr, selector string) (string, error)
	// Listen subscribes a client to the endpoint's announcements for the selector; the channel receives one
	// value per announcement. Set by endpoints that announce changes.
	Listen func(ctx context.Context, addr, selector string) (<-chan struct{}, error)
	// SendEvents sends a batch as an in-process provider would. Set by endpoints that collect events.
	SendEvents func(ctx context.Context, addr string, batch Batch) error
}

// Run checks the factory against every applicable case of the contract: one subtest per case.
func Run(t *testing.T, f service.Factory, in Inputs) {
	t.Helper()
	Check(t, f, in).Run(t)
}

type contractCase struct {
	name       string
	applicable func(service.Needs, Inputs) bool
	run        func(*env) error
	// feature is set for a case of an optional feature: it runs when the input is supplied, is left out when
	// the feature is named in Inputs.Without, and fails otherwise.
	feature Feature
}

// supplied reports whether the input of the feature is set.
func (f Feature) supplied(in Inputs) bool {
	switch f {
	case FeatureDocuments:
		return in.Documents != nil
	case FeatureValidator:
		return in.Validator != nil
	case FeatureListen:
		return in.Listen != nil
	case FeatureSendEvents:
		return in.SendEvents != nil
	case FeatureHTTP:
		return in.HTTP
	}
	return false
}

var always = func(service.Needs, Inputs) bool { return true }

func resolves(n service.Needs, _ Inputs) bool { return n.Router || n.Format != "" }

// Check runs the cases and returns the report, without failing a test. A case of a feature the author named in Without
// is reported as not applicable; one whose input is neither supplied nor named fails, naming the input. A feature both
// supplied and named in Without, or a name that is no feature, aborts the run without a case (UC-008 BR-006).
func Check(_ testing.TB, f service.Factory, in Inputs) contracttest.Report {
	cases := []contractCase{
		{"declarations", always, caseDeclarations, ""},
		{"creates from what it declared", always, caseCreates, ""},
		{"serves and returns within the budget with its connections ended", always, caseServesAndStops, ""},
		{"a serve error is returned", always, caseServeError, ""},
		{"resolves through the injected resolver with the effective selector", resolves, caseResolvesThroughInjection, ""},
		{"publishes one evaluation event per resolved flag", resolves, caseOneEvaluationEvent, ""},
		{"serves the documents of the requested format, unchanged, with versions", always, caseDocuments, FeatureDocuments},
		{"the validator follows the document version", always, caseValidator, FeatureValidator},
		{"reacts to a configuration change event for its selector only", always, caseReacts, FeatureListen},
		{"relays events marked in-process", always, caseRelays, FeatureSendEvents},
		{"honours the bound selector and the client's selector", func(n service.Needs, _ Inputs) bool { return n.Router },
			caseEffectiveSelector, FeatureHTTP},
	}

	needs := f.Needs()
	var report contracttest.Report
	supplied := map[Feature]bool{}
	for _, feature := range allFeatures {
		supplied[feature] = feature.supplied(in)
	}
	if err := contracttest.ValidateWithout(in.Without, supplied); err != nil {
		return contracttest.Report{Aborted: err.Error()}
	}
	for _, c := range cases {
		if !c.applicable(needs, in) {
			continue
		}
		if c.feature != "" && !c.feature.supplied(in) {
			if slices.Contains(in.Without, c.feature) {
				report.Cases = append(report.Cases, contracttest.NotApplicable(c.name, fmt.Sprintf("Without: %s", c.feature)))
				continue
			}
			report.Cases = append(report.Cases, failure(c.name, fmt.Errorf(
				"the input %s is required\nexpected the endpoint to supply it, or to name %s in Without when it has no such feature\nobserved: neither",
				c.feature, c.feature)))
			continue
		}
		if err := guarded(func() error { return c.run(newEnv(f, in)) }); err != nil {
			report.Cases = append(report.Cases, failure(c.name, err))
			continue
		}
		report.Cases = append(report.Cases, contracttest.Pass(c.name, c.name))
	}
	return report
}

// failure makes a failed case from an error that states the expected and the observed behaviour, each on a line of its
// own.
func failure(name string, err error) contracttest.Case {
	text := err.Error()
	if i := strings.Index(text, "\nobserved"); i >= 0 {
		return contracttest.Case{Name: name, Expected: strings.TrimSpace(text[:i]), Observed: strings.TrimSpace(text[i:])}
	}
	return contracttest.Case{Name: name, Expected: name, Observed: text}
}

// guarded turns a panic of the endpoint into a failure of the case.
func guarded(run func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("expected no panic\nobserved: %v", r)
		}
	}()
	return run()
}

// env is the harness of one case: a store, a bus and what the factory declared it needs.
type env struct {
	f     service.Factory
	in    Inputs
	store *store.Store
	bus   *events.Bus
	rec   *recordingEvaluator
	log   *logger.Logger
	// tcpReady makes start wait for the port instead of a client connection, for a case in which every request
	// fails by design
	tcpReady bool
}

func newEnv(f service.Factory, in Inputs) *env {
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{source})
	e := &env{f: f, in: in, store: st, bus: events.NewBus(), rec: &recordingEvaluator{}, log: log}
	if err != nil {
		panic(err)
	}
	e.putDocuments("v1")
	return e
}

// putDocuments writes one document of flagd's format and one of a vendor format, at version.
func (e *env) putDocuments(version string) {
	err := e.store.Apply(store.ApplyRequest{
		Format: "flagd", Source: source,
		Flags:       []model.Flag{{Key: flagKey, State: "ENABLED", DefaultVariant: "on", Variants: map[string]any{"on": true}, FlagSetId: flagSet, Source: source}},
		SetMetadata: model.Metadata{"flagSetId": flagSet},
		Documents: []store.Document{
			{Format: "flagd", Source: source, FlagSet: flagSet, Version: version, FlagKeys: []string{flagKey}, Value: "flagd-" + version},
		},
	})
	if err == nil {
		err = e.store.Apply(store.ApplyRequest{
			Format: formatName, Source: source,
			Documents: []store.Document{
				{Format: formatName, Source: source, FlagSet: flagSet, Version: version, FlagKeys: []string{"vendor-flag"}, Value: "vendor-" + version},
			},
		})
	}
	if err != nil {
		panic(err)
	}
}

// change writes the documents at version and publishes the configuration change event for flag set.
func (e *env) change(flagSetID, version string) {
	e.putDocuments(version)
	events.PublishConfigurationChange(e.bus, events.ConfigurationChange{
		Time: time.Now(), Source: source, Format: "flagd", FlagSet: flagSetID, Version: version, Updated: []string{flagKey},
	})
}

// settings builds what the runtime would inject for the declared needs.
func (e *env) settings(mutate func(*service.Settings)) service.Settings {
	initialized := make(chan struct{})
	close(initialized)
	s := service.Settings{
		Store: e.store, Bus: e.bus, Initialized: initialized, Logger: e.log, Config: maps(e.in.Config),
	}
	needs := e.f.Needs()
	if needs.Router {
		s.Resolver = typed.New(e.rec, typed.WithObserver(events.EvaluationObserver(e.bus, e.f.Type())))
	}
	if needs.Format != "" {
		s.Evaluator = e.rec
	}
	if e.in.HTTP {
		s.HTTPMiddleware = bindingMiddleware
	}
	if mutate != nil {
		mutate(&s)
	}
	return s
}

func maps(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// running is an endpoint serving on a free port.
type running struct {
	addr   string
	cancel context.CancelFunc
	done   chan error
	ended  <-chan struct{}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (e *env) create(port int, mutate func(*service.Settings)) (service.Endpoint, string, error) {
	s := e.settings(func(s *service.Settings) {
		if e.in.PortKey != "" {
			s.Config[e.in.PortKey] = port
		}
		if mutate != nil {
			mutate(s)
		}
	})
	addr := ""
	if e.in.PortKey != "" {
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	}
	endpoint, err := e.f.Create(s)
	if err == nil && endpoint == nil {
		err = errors.New("Create returned neither an endpoint nor an error")
	}
	return endpoint, addr, err
}

// start creates the endpoint, serves it and waits until a client can connect.
func (e *env) start(mutate func(*service.Settings)) (*running, error) {
	port := 0
	if e.in.PortKey != "" {
		var err error
		if port, err = freePort(); err != nil {
			return nil, err
		}
	}
	endpoint, addr, err := e.create(port, mutate)
	if err != nil {
		return nil, fmt.Errorf("expected Create to succeed with what the factory declared\nobserved: %w", err)
	}
	if e.in.Connect == nil {
		return nil, errors.New("the input Connect is required: the harness connects to learn that the endpoint serves")
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := &running{addr: addr, cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- endpoint.Serve(ctx) }()

	deadline := time.Now().Add(Budget)
	for {
		var err error
		if e.tcpReady && addr != "" {
			// every request fails by design, Connect included: the port accepting a connection is all there is to wait for
			var conn net.Conn
			if conn, err = net.DialTimeout("tcp", addr, time.Second); err == nil {
				conn.Close()
				return r, nil
			}
		} else {
			var ended <-chan struct{}
			if ended, err = e.in.Connect(context.Background(), addr); err == nil {
				r.ended = ended
				return r, nil
			}
		}
		select {
		case serveErr := <-r.done:
			cancel()
			return nil, fmt.Errorf("expected Serve to keep serving\nobserved: it returned %v", serveErr)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			return nil, fmt.Errorf("expected a client to connect within %v\nobserved: %w", Budget, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (r *running) stop() error {
	r.cancel()
	select {
	case err := <-r.done:
		if err != nil {
			return fmt.Errorf("expected Serve to return nil when its context ends\nobserved: %w", err)
		}
	case <-time.After(Budget):
		return fmt.Errorf("expected Serve to return within %v of its context ending\nobserved: it was still serving", Budget)
	}
	return nil
}

func (e *env) call() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}

func caseDeclarations(e *env) error {
	if err := service.ValidateDeclarations(e.f); err != nil {
		return fmt.Errorf("expected valid declarations\nobserved: %w", err)
	}
	return nil
}

func caseCreates(e *env) error {
	needs := e.f.Needs()
	if _, _, err := e.create(0, nil); err != nil {
		return fmt.Errorf("expected Create to succeed with exactly what the factory declared (resolver: %t, evaluator for %q)\nobserved: %w",
			needs.Router, needs.Format, err)
	}
	return nil
}

func caseServesAndStops(e *env) error {
	r, err := e.start(nil)
	if err != nil {
		return err
	}
	if err := r.stop(); err != nil {
		return err
	}
	select {
	case <-r.ended:
	case <-time.After(Budget):
		return fmt.Errorf("expected the endpoint to end an open client connection when its context ends\nobserved: the connection was still open after %v", Budget)
	}
	return nil
}

func caseServeError(e *env) error {
	if e.in.PortKey == "" {
		return errors.New("the input PortKey is required: the harness occupies the endpoint's port to see Serve fail")
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	busy, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	defer busy.Close()

	endpoint, _, err := e.create(port, nil)
	if err != nil {
		// failing to listen in Create is also an error that stops the distribution
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- endpoint.Serve(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			return errors.New("expected Serve to return an error when its port is in use\nobserved: it returned nil")
		}
	case <-time.After(Budget):
		return fmt.Errorf("expected Serve to return an error when its port is in use\nobserved: it was still serving after %v", Budget)
	}
	return nil
}

func (e *env) resolve(r *running, key, selector string) (Answer, error) {
	if e.in.Resolve == nil {
		return Answer{}, errors.New("the input Resolve is required: the factory declares needs")
	}
	ctx, cancel := e.call()
	defer cancel()
	return e.in.Resolve(ctx, r.addr, key, selector)
}

func caseResolvesThroughInjection(e *env) error {
	r, err := e.start(nil)
	if err != nil {
		return err
	}
	defer r.stop() //nolint:errcheck // stopping is the case of its own
	if _, err := e.resolve(r, flagKey, "flagSetId="+flagSet); err != nil {
		return fmt.Errorf("expected the endpoint to resolve %q\nobserved: %w", flagKey, err)
	}
	seen := e.rec.requests()
	if len(seen) != 1 || seen[0].FlagKey != flagKey || seen[0].Selector.FlagSetId() != flagSet {
		return fmt.Errorf("expected one evaluation of %q for the client's selector flagSetId=%s through the injected resolver\nobserved: %d evaluations, %+v",
			flagKey, flagSet, len(seen), seen)
	}
	return nil
}

func caseOneEvaluationEvent(e *env) error {
	sub := e.bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindEvaluation}, Buffer: 16})
	defer sub.Close()
	r, err := e.start(nil)
	if err != nil {
		return err
	}
	defer r.stop() //nolint:errcheck // stopping is the case of its own

	for _, key := range []string{flagKey, missingKey} {
		if _, err := e.resolve(r, key, ""); err != nil {
			return fmt.Errorf("expected the endpoint to answer for %q\nobserved: %w", key, err)
		}
	}
	got := drain(sub)
	if len(got) != 2 || got[0].FlagKey != flagKey || got[1].FlagKey != missingKey {
		return fmt.Errorf("expected one evaluation event for each of the two resolved flags, %q and %q\nobserved: %d events: %+v", flagKey, missingKey, len(got), got)
	}
	if got[1].ErrorCode != model.FlagNotFoundErrorCode {
		return fmt.Errorf("expected the event of %q to carry the error code %s\nobserved: %q", missingKey, model.FlagNotFoundErrorCode, got[1].ErrorCode)
	}
	if got[0].Origin != events.OriginService {
		return fmt.Errorf("expected origin %q\nobserved: %q", events.OriginService, got[0].Origin)
	}
	return nil
}

func drain(sub *events.Subscription) []events.Evaluation {
	time.Sleep(100 * time.Millisecond)
	var out []events.Evaluation
	for {
		select {
		case ev := <-sub.C():
			if evaluation, ok := ev.(events.Evaluation); ok {
				out = append(out, evaluation)
			}
		default:
			return out
		}
	}
}

func caseDocuments(e *env) error {
	r, err := e.start(nil)
	if err != nil {
		return err
	}
	defer r.stop() //nolint:errcheck // stopping is the case of its own

	for _, format := range []string{"flagd", formatName} {
		ctx, cancel := e.call()
		got, err := e.in.Documents(ctx, r.addr, format, "flagSetId="+flagSet)
		cancel()
		if err != nil {
			return fmt.Errorf("expected the documents of format %q\nobserved: %w", format, err)
		}
		want := e.store.Documents(format, source)
		if len(got) != len(want) {
			return fmt.Errorf("expected %d document(s) of format %q only\nobserved: %d: %+v", len(want), format, len(got), got)
		}
		for i := range want {
			if got[i].Format != format || got[i].Version != want[i].Version || !reflect.DeepEqual(got[i].Value, want[i].Value) {
				return fmt.Errorf("expected the document unchanged, version %q, value %v\nobserved: format %q, version %q, value %v",
					want[i].Version, want[i].Value, got[i].Format, got[i].Version, got[i].Value)
			}
		}
	}
	return nil
}

func (e *env) validator(r *running) (string, error) {
	ctx, cancel := e.call()
	defer cancel()
	return e.in.Validator(ctx, r.addr, "flagSetId="+flagSet)
}

func caseValidator(e *env) error {
	r, err := e.start(nil)
	if err != nil {
		return err
	}
	defer r.stop() //nolint:errcheck // stopping is the case of its own

	first, err := e.validator(r)
	if err != nil {
		return fmt.Errorf("expected a validator\nobserved: %w", err)
	}
	again, err := e.validator(r)
	if err != nil || again != first {
		return fmt.Errorf("expected the validator not to change while the documents do not\nobserved: %q then %q (%v)", first, again, err)
	}
	e.change(flagSet, "v2")
	deadline := time.Now().Add(2 * time.Second)
	for {
		next, err := e.validator(r)
		if err == nil && next != first {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("expected the validator to change with the document version\nobserved: %q before and after version v2 (%v)", first, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func caseReacts(e *env) error {
	r, err := e.start(nil)
	if err != nil {
		return err
	}
	defer r.stop() //nolint:errcheck // stopping is the case of its own

	ctx, cancel := e.call()
	defer cancel()
	announced, err := e.in.Listen(ctx, r.addr, "flagSetId="+flagSet)
	if err != nil {
		return fmt.Errorf("expected the endpoint to accept a listener\nobserved: %w", err)
	}
	time.Sleep(100 * time.Millisecond)

	e.change(otherSet, "v2")
	select {
	case <-announced:
		return errors.New("expected no announcement for a change that does not concern the client's selector\nobserved: an announcement")
	case <-time.After(300 * time.Millisecond):
	}

	e.change(flagSet, "v3")
	select {
	case <-announced:
	case <-time.After(2 * time.Second):
		return errors.New("expected an announcement for a change that concerns the client's selector\nobserved: none within 2s")
	}

	// A subscription that missed events cannot tell what changed: the endpoint must reread the store, whatever
	// the event says. The harness changes the documents without an event, then publishes an event for another
	// flag set that carries a count of missed events.
	e.putDocuments("silent")
	events.PublishConfigurationChange(e.bus, events.ConfigurationChange{
		Source: source, Format: "flagd", FlagSet: otherSet, Version: "silent", Missed: 3,
	})
	select {
	case <-announced:
	case <-time.After(2 * time.Second):
		return errors.New("expected an announcement after a configuration change event that reports missed events, the documents behind the client's selector having changed unannounced\nobserved: none within 2s; the endpoint did not reread the store")
	}
	return nil
}

func caseRelays(e *env) error {
	sub := e.bus.Subscribe(events.SubscribeOptions{
		Kinds: []events.Kind{events.KindEvaluation, events.KindTracking}, Buffer: 16,
	})
	defer sub.Close()
	r, err := e.start(nil)
	if err != nil {
		return err
	}
	defer r.stop() //nolint:errcheck // stopping is the case of its own

	ctx, cancel := e.call()
	defer cancel()
	batch := Batch{
		Evaluations: []events.Evaluation{{FlagKey: flagKey, Reason: model.StaticReason, Origin: events.OriginService}},
		Trackings:   []events.Tracking{{EventName: "purchase", TargetingKey: "user", Origin: events.OriginService}},
	}
	if err := e.in.SendEvents(ctx, r.addr, batch); err != nil {
		return fmt.Errorf("expected the endpoint to accept a batch\nobserved: %w", err)
	}
	time.Sleep(200 * time.Millisecond)
	var evaluations, trackings int
	for {
		select {
		case ev := <-sub.C():
			switch v := ev.(type) {
			case events.Evaluation:
				evaluations++
				if v.Origin != events.OriginInProcess {
					return fmt.Errorf("expected the relayed evaluation event to carry origin %q\nobserved: %q", events.OriginInProcess, v.Origin)
				}
			case events.Tracking:
				trackings++
				if v.Origin != events.OriginInProcess {
					return fmt.Errorf("expected the relayed tracking event to carry origin %q\nobserved: %q", events.OriginInProcess, v.Origin)
				}
			}
			continue
		default:
		}
		break
	}
	if evaluations != 1 || trackings != 1 {
		return fmt.Errorf("expected one evaluation and one tracking event on the bus\nobserved: %d and %d", evaluations, trackings)
	}
	return nil
}

func caseEffectiveSelector(e *env) error {
	rows := []struct {
		name, bound, client string
		wantSet, wantSource string
		noFlag              bool
		// invalid is a bound selector the harness cannot parse: nothing may be evaluated and the client must
		// not be answered as if nothing restricted it
		invalid bool
	}{
		{name: "bound only", bound: "flagSetId=" + flagSet, wantSet: flagSet},
		{name: "client only", client: "flagSetId=" + flagSet, wantSet: flagSet},
		{name: "narrowing", bound: "flagSetId=" + flagSet, client: "source=" + source, wantSet: flagSet, wantSource: source},
		{name: "conflict", bound: "flagSetId=" + flagSet, client: "flagSetId=" + otherSet, noFlag: true},
		{name: "invalid bound selector", bound: "colour=red", client: "source=" + source, invalid: true},
	}
	for _, row := range rows {
		e.rec.reset()
		bound := row.bound
		e.tcpReady = row.invalid
		r, err := e.start(func(s *service.Settings) {
			s.HTTPMiddleware = func(next http.Handler) http.Handler { return bindingMiddlewareFor(bound, next) }
		})
		if err != nil {
			return err
		}
		answer, err := e.resolve(r, flagKey, row.client)
		stopErr := r.stop()
		if row.invalid {
			if seen := e.rec.requests(); len(seen) != 0 || (err == nil && answer.ErrorCode == "") {
				return fmt.Errorf("selector row %q: expected the request to fail without evaluating, the bound selector is invalid\nobserved: %d evaluations, error code %q, error %v",
					row.name, len(seen), answer.ErrorCode, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("selector row %q: expected an answer\nobserved: %w", row.name, err)
		}
		if stopErr != nil {
			return stopErr
		}
		seen := e.rec.requests()
		if row.noFlag {
			if answer.ErrorCode != model.FlagNotFoundErrorCode || len(seen) != 0 {
				return fmt.Errorf("selector row %q: expected %s without evaluating, the bound and the client's selector name different flag sets\nobserved: %q, %d evaluations",
					row.name, model.FlagNotFoundErrorCode, answer.ErrorCode, len(seen))
			}
			continue
		}
		if len(seen) != 1 || seen[0].Selector.FlagSetId() != row.wantSet || seen[0].Selector.Source() != row.wantSource {
			return fmt.Errorf("selector row %q: expected one evaluation for flagSetId=%q source=%q\nobserved: %+v", row.name, row.wantSet, row.wantSource, seen)
		}
		if seen[0].Attributes["audience"] != "contract" {
			return fmt.Errorf("selector row %q: expected the request attributes bound by the middleware to reach the resolver\nobserved: %v", row.name, seen[0].Attributes)
		}
	}
	return nil
}
