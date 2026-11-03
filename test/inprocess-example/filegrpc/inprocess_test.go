package inprocess_test

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	v1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	syncgrpc "github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc"
	"github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc/credentials"
	"google.golang.org/grpc"

	inprocess "github.com/open-feature/flagd/test/inprocess-example/filegrpc"
)

const flags = `{"flags":{"on-flag":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`

// forbidden is what an embedded evaluator keeps out of a provider's module graph, except for gRPC, the
// transport of the sync provider this provider chose, and the OpenTelemetry modules gRPC's own go.mod requires:
// platform clients, the telemetry module and every other sync provider.
var forbidden = regexp.MustCompile(`k8s.io|gocloud.dev|` +
	`openfeature-relay/(telemetry|sync/(file|http|openfeature-operator|blob|gcs|azblob|s3|builder))`)

func goList(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
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

// TestModuleGraph: adding the gRPC sync provider adds gRPC and the sync provider to the module graph, and
// nothing else an embedded evaluator keeps out. gRPC's go.mod requires OpenTelemetry modules, so the
// module graph names them, but the packages the provider builds contain no OpenTelemetry (Q2).
func TestModuleGraph(t *testing.T) {
	graph := goList(t, "-m", "all")
	for _, line := range strings.Split(graph, "\n") {
		if module := strings.Fields(line); len(module) > 0 && forbidden.MatchString(module[0]) {
			t.Errorf("the module graph carries %s", module[0])
		}
	}
	for _, want := range []string{"google.golang.org/grpc ", "openfeature-relay/sync/flagd-grpc ", "openfeature-relay/evaluator/flagd "} {
		if !strings.Contains(graph, want) {
			t.Errorf("the module graph lacks %q", want)
		}
	}

	for _, pkg := range strings.Split(goList(t, "-deps", "."), "\n") {
		if strings.Contains(pkg, "go.opentelemetry.io") {
			t.Errorf("the packages of the provider contain %s", pkg)
		}
	}
}

// syncServer streams one flag configuration, as flagd-full and a configuration proxy do for a flag sync
// request of flagd's configuration format, the default (BR-007).
type syncServer struct {
	syncv1grpc.UnimplementedFlagSyncServiceServer
}

func (syncServer) SyncFlags(_ *v1.SyncFlagsRequest, stream syncv1grpc.FlagSyncService_SyncFlagsServer) error {
	if err := stream.Send(&v1.SyncFlagsResponse{FlagConfiguration: flags}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
}

// TestGrpcSyncFeedsFormat covers steps 5 and 6 with the gRPC sync provider: its output feeds the configuration
// format. The provider's flag sync request names no configuration format, so flagd's applies.
func TestGrpcSyncFeedsFormat(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	syncv1grpc.RegisterFlagSyncServiceServer(server, syncServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	addr := listener.Addr().String()
	log := logger.NewLogger(nil, false)
	syncer := &syncgrpc.Sync{URI: addr, Logger: log, CredentialBuilder: &credentials.CredentialBuilder{}}
	p, err := inprocess.New(log, addr, syncer)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan configformat.Result, 4)
	p.OnChange = func(r configformat.Result) { changes <- r }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-changes:
		if len(r.Documents) != 1 || strings.Join(r.Documents[0].Added, ",") != "on-flag" {
			t.Fatalf("want one document that adds on-flag, got %+v", r.Documents)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no flag configuration arrived")
	}
	if got := p.Boolean(ctx, "on-flag", false, nil); !got.Value || got.Reason != "STATIC" {
		t.Fatalf("on-flag: want true with reason STATIC, got %+v", got)
	}
}
