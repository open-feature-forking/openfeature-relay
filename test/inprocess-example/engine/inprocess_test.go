package inprocess_test

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"

	inprocess "github.com/open-feature/flagd/test/inprocess-example/engine"
)

// forbidden is what an embedded engine keeps out of a provider's package graph (BR-001): telemetry, transport and
// platform clients, the JsonLogic engine, and every flagd and sync module. The engine module's own tests run a
// distribution, so its module graph carries flagd's runtime; its packages do not.
var forbidden = regexp.MustCompile(`opentelemetry|google.golang.org/grpc|connectrpc.com|k8s.io|gocloud.dev|jsonlogic|` +
	`openfeature-relay/(telemetry|sync/|evaluator/flagd|configformat/flagd)|open-feature/flagd/flagd`)

type channelSync struct {
	source   string
	payloads chan string
}

func (channelSync) Init(context.Context) error                             { return nil }
func (channelSync) ReSync(context.Context, chan<- coresync.DataSync) error { return nil }
func (channelSync) IsReady() bool                                          { return true }

func (c channelSync) Sync(ctx context.Context, out chan<- coresync.DataSync) error {
	for {
		select {
		case data := <-c.payloads:
			out <- coresync.DataSync{Source: c.source, FlagData: data}
		case <-ctx.Done():
			return nil
		}
	}
}

func newProvider(t *testing.T) (*inprocess.Provider, channelSync, chan configformat.Result, chan error) {
	t.Helper()
	syncer := channelSync{source: "custom://flags", payloads: make(chan string, 4)}
	p, err := inprocess.New(logger.NewLogger(nil, false), syncer.source, syncer)
	if err != nil {
		t.Fatal(err)
	}
	changes, errs := make(chan configformat.Result, 4), make(chan error, 4)
	p.OnChange = func(r configformat.Result) { changes <- r }
	p.OnError = func(err error) { errs <- err }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return p, syncer, changes, errs
}

func waitFor[T any](t *testing.T, ch chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("nothing arrived")
	}
	var zero T
	return zero
}

// TestModuleGraph covers A2's step 2: the engine module and the core module bring no telemetry provider,
// service endpoint, transport or sync provider code, and no flagd configuration format or evaluator, into the
// provider (BR-001).
func TestModuleGraph(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./...").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("go list: %v\n%s", err, exit.Stderr)
		}
		t.Fatal(err)
	}
	var found []string
	for _, pkg := range strings.Fields(string(out)) {
		if forbidden.MatchString(pkg) {
			found = append(found, pkg)
		}
	}
	if len(found) > 0 {
		t.Fatalf("the provider imports packages an embedded engine keeps out: %v", found)
	}
	if !strings.Contains(string(out), "configformat-example/evaluator") || !strings.Contains(string(out), "openfeature-relay/core/pkg/evaluator/typed") {
		t.Fatal("the provider lacks the engine module's evaluator or the core module's typed adapter")
	}
}

// TestResolveThroughAdapter covers steps 7 and 8 with a vendor engine: the flags the engine's configuration format
// accepted resolve through the engine's evaluator and the core module's typed adapter, which also owns the type
// check (A6).
func TestResolveThroughAdapter(t *testing.T) {
	p, syncer, changes, errs := newProvider(t)

	syncer.payloads <- `{"environment":"prod","flags":{"greeting":"hello","enabled":true,"server.secret":"hush"}}`
	if change := waitFor(t, changes); len(change.Documents) != 1 || change.Documents[0].FlagSet != "prod" {
		t.Fatalf("want one document of the prod environment, got %+v", change.Documents)
	}

	if got := p.String(context.Background(), "greeting", "default", nil); got.Value != "hello" || got.Reason != "STATIC" || got.ErrorCode != "" {
		t.Fatalf("greeting: want hello with reason STATIC, got %+v", got)
	}
	if got := p.Boolean(context.Background(), "greeting", true, nil); !got.Value || got.ErrorCode != "TYPE_MISMATCH" || got.Reason != "ERROR" {
		t.Fatalf("greeting as boolean: want the default with TYPE_MISMATCH and ERROR, got %+v", got)
	}
	if got := p.Boolean(context.Background(), "unknown", true, nil); !got.Value || got.ErrorCode != "FLAG_NOT_FOUND" {
		t.Fatalf("unknown flag: want the default with FLAG_NOT_FOUND, got %+v", got)
	}

	syncer.payloads <- `{"environment":"prod","flags":{"greeting":null}}`
	if err := waitFor(t, errs); err == nil {
		t.Fatal("want the engine's configuration format to reject a null value")
	}
	if got := p.String(context.Background(), "greeting", "default", nil); got.Value != "hello" {
		t.Fatalf("greeting: want the flags loaded before to stay, got %+v", got)
	}
}
