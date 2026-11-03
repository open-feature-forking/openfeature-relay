package inprocess_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	syncfile "github.com/open-feature-forking/openfeature-relay/sync/file"

	inprocess "github.com/open-feature/flagd/test/inprocess-example/file"
)

const (
	flagsV1 = `{"flags":{
		"on-flag":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}},
		"off-flag":{"state":"DISABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`
	flagsV2 = `{"flags":{
		"on-flag":{"state":"ENABLED","defaultVariant":"off","variants":{"on":true,"off":false}},
		"off-flag":{"state":"DISABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`
	flagsInvalid = `{"flags":{"on-flag":{"state":"ENABLED","defaultVariant":"missing","variants":{"on":true}}}}`
)

// forbidden is what an embedded evaluator keeps out of a provider's module graph (BR-001, BR-004): telemetry,
// transport and platform clients, and every sync provider but the file one this provider chose.
var forbidden = regexp.MustCompile(`opentelemetry|google.golang.org/grpc|connectrpc.com|k8s.io|gocloud.dev|` +
	`openfeature-relay/(telemetry|sync/(flagd-grpc|http|openfeature-operator|blob|gcs|azblob|s3|builder))`)

type observer struct {
	changes chan configformat.Result
	errs    chan error
}

func newProvider(t *testing.T, source string, syncer coresync.ISync) (*inprocess.Provider, *observer) {
	t.Helper()
	p, err := inprocess.New(logger.NewLogger(nil, false), source, syncer)
	if err != nil {
		t.Fatal(err)
	}
	o := &observer{changes: make(chan configformat.Result, 16), errs: make(chan error, 16)}
	p.OnChange = func(r configformat.Result) { o.changes <- r }
	p.OnError = func(err error) { o.errs <- err }

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return p, o
}

func (o *observer) nextChange(t *testing.T) configformat.Result {
	t.Helper()
	select {
	case r := <-o.changes:
		return r
	case err := <-o.errs:
		t.Fatalf("flag configuration rejected: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("no flag configuration arrived")
	}
	return configformat.Result{}
}

func (o *observer) nextError(t *testing.T) error {
	t.Helper()
	select {
	case err := <-o.errs:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("no flag configuration was rejected")
	}
	return nil
}

func writeFlags(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// goList runs the go command in the module at dir, outside the workspace, as an embedder would.
func goList(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("go list %v: %v\n%s", args, err, exit.Stderr)
		}
		t.Fatalf("go list %v: %v", args, err)
	}
	return string(out)
}

// violations lists the modules of the module graph at dir that match the pattern.
func violations(t *testing.T, dir string, pattern *regexp.Regexp) []string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(goList(t, dir, "-m", "all"), "\n") {
		if module := strings.Fields(line); len(module) > 0 && pattern.MatchString(module[0]) {
			found = append(found, module[0])
		}
	}
	return found
}

// TestModuleGraph covers steps 1 to 4: adding the configuration format, the evaluator, the core module and
// the file sync provider leaves telemetry, transport, platform clients and every other sync provider out of
// the provider's module graph (BR-001, BR-004).
func TestModuleGraph(t *testing.T) {
	if found := violations(t, ".", forbidden); len(found) > 0 {
		t.Fatalf("the module graph carries modules an embedded evaluator keeps out: %v", found)
	}

	graph := goList(t, ".", "-m", "all")
	for _, want := range []string{
		"openfeature-relay/core ", "openfeature-relay/configformat/flagd ", "openfeature-relay/evaluator/flagd ", "openfeature-relay/sync/file ",
	} {
		if !strings.Contains(graph, want) {
			t.Errorf("the module graph lacks %q", want)
		}
	}
}

// TestFileSyncFeedsFormat covers steps 5 and 6: the output of the file sync provider feeds the configuration
// format, which keeps the flags as documents in the store and reports which of them changed.
func TestFileSyncFeedsFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flags.json")
	writeFlags(t, path, flagsV1)
	log := logger.NewLogger(nil, false)
	syncer, err := syncfile.NewSyncProvider(path, syncfile.FSNOTIFY, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, o := newProvider(t, path, syncer)

	first := o.nextChange(t)
	if len(first.Documents) != 1 || strings.Join(first.Documents[0].Added, ",") != "off-flag,on-flag" {
		t.Fatalf("want one document that adds off-flag and on-flag, got %+v", first.Documents)
	}
	if got := p.Boolean(context.Background(), "on-flag", false, nil); !got.Value || got.Reason != "STATIC" || got.ErrorCode != "" {
		t.Fatalf("on-flag: want true with reason STATIC, got %+v", got)
	}
	if docs := p.Store().Documents("flagd", path); len(docs) != 1 || docs[0].Version != first.Documents[0].Version {
		t.Fatalf("the store holds the document the format reported, got %+v", docs)
	}

	writeFlags(t, path, flagsV2)
	second := o.nextChange(t)
	if got := strings.Join(second.Documents[0].Updated, ","); got != "on-flag" {
		t.Fatalf("want on-flag updated, got %q", got)
	}
	if second.Documents[0].Version == first.Documents[0].Version {
		t.Fatal("a changed flag configuration needs a new document version")
	}
	if got := p.Boolean(context.Background(), "on-flag", true, nil); got.Value {
		t.Fatalf("on-flag: want false after the change, got %+v", got)
	}
}

// channelSync is a sync provider of the provider's own, written against the sync interface of the core module.
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

// TestCustomSyncProvider covers A3: a source no official sync provider supports gets a sync provider in the
// provider's own module, and the flow continues at step 5.
func TestCustomSyncProvider(t *testing.T) {
	syncer := channelSync{source: "custom://flags", payloads: make(chan string, 4)}
	p, o := newProvider(t, syncer.source, syncer)

	syncer.payloads <- flagsV1
	o.nextChange(t)

	if got := p.Boolean(context.Background(), "on-flag", false, nil); !got.Value {
		t.Fatalf("on-flag: want true, got %+v", got)
	}
}

// TestInvalidConfigurationKeepsFlags covers A4: the configuration format rejects a flag configuration that does
// not validate, keeps the flags loaded before and reports the error to the provider.
func TestInvalidConfigurationKeepsFlags(t *testing.T) {
	syncer := channelSync{source: "custom://flags", payloads: make(chan string, 4)}
	p, o := newProvider(t, syncer.source, syncer)

	syncer.payloads <- flagsV1
	o.nextChange(t)
	syncer.payloads <- flagsInvalid

	err := o.nextError(t)
	var rejection configformat.Rejection
	if !errors.As(err, &rejection) || rejection.Source != syncer.source || rejection.Entry != "on-flag" {
		t.Fatalf("want a rejection naming the source and on-flag, got %v", err)
	}
	if got := p.Boolean(context.Background(), "on-flag", false, nil); !got.Value || got.Reason != "STATIC" {
		t.Fatalf("on-flag: want the flags loaded before to stay, got %+v", got)
	}
}

// TestUnresolvableFlags covers the part of A6 that does not involve the typed adapter: an unknown flag and a
// disabled one resolve to the caller's default with the error code and reason remote evaluation returns.
func TestUnresolvableFlags(t *testing.T) {
	syncer := channelSync{source: "custom://flags", payloads: make(chan string, 4)}
	p, o := newProvider(t, syncer.source, syncer)
	syncer.payloads <- flagsV1
	o.nextChange(t)

	missing := p.Boolean(context.Background(), "no-such-flag", true, nil)
	if !missing.Value || missing.ErrorCode != "FLAG_NOT_FOUND" || missing.Reason != "ERROR" {
		t.Fatalf("unknown flag: want the default with FLAG_NOT_FOUND and ERROR, got %+v", missing)
	}
	disabled := p.Boolean(context.Background(), "off-flag", true, nil)
	if !disabled.Value || disabled.Reason != "DISABLED" || disabled.ErrorCode != "" {
		t.Fatalf("disabled flag: want the default with reason DISABLED, got %+v", disabled)
	}
}

// TestBoundaryCatchesForbiddenDependency covers A1: a provider whose module graph gains a transport or
// telemetry module through a sync provider fails the same check. The gRPC example next to this module is
// such a provider.
func TestBoundaryCatchesForbiddenDependency(t *testing.T) {
	found := violations(t, "../filegrpc", forbidden)
	if len(found) == 0 {
		t.Fatal("the check must report the modules the gRPC example carries")
	}
	joined := strings.Join(found, " ")
	for _, want := range []string{"google.golang.org/grpc", "openfeature-relay/sync/flagd-grpc"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the check must name %s among %v", want, found)
		}
	}
}
