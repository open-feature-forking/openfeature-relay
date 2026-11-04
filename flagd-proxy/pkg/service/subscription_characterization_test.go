//go:build !race

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	syncgrpc "buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	syncpb "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature/flagd/flagd-proxy/pkg/service/subscriptions"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// These tests run without the race detector: the manager reports a data race between dropping a request's subscription
// and delivering to it (subscriptions/manager.go and multiplexer.go), and flagd-proxy's code is not changed in step 10.

// startProxy serves flagd-proxy's flag sync on free ports until the test binary ends (Serve races on its metrics server
// field when its context ends) and returns a client of it.
func startProxy(t *testing.T) syncgrpc.FlagSyncServiceClient {
	t.Helper()
	ctx := context.Background()
	log := logger.NewLogger(nil, false)
	s := NewServer(ctx, log, subscriptions.NewManager(ctx, log))
	port, managementPort := freePort(t), freePort(t)
	go func() {
		_ = s.Serve(ctx, service.Configuration{Port: port, ManagementPort: managementPort, ReadinessProbe: func() bool { return true }})
	}()
	addr := "127.0.0.1:" + fmt.Sprint(port)
	waitForListener(t, addr)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("creating the client: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return syncgrpc.NewFlagSyncServiceClient(conn)
}

func writeFlags(t *testing.T) (uri, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flags.json")
	content = `{"flags":{"a":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return "file:" + path, content
}

// TestSubscriptionBySourceURI pins how flagd-proxy takes a subscription: the selector field of a request is the URI of
// the source, not a selector expression, and SyncFlags answers with the source's flag configuration. Step 10's flag
// sync endpoint reads selectors differently (UC-017 BR-007), so nothing of it may reach flagd-proxy.
func TestSubscriptionBySourceURI(t *testing.T) {
	client := startProxy(t)
	uri, flags := writeFlags(t)

	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	stream, err := client.SyncFlags(ctx, &syncpb.SyncFlagsRequest{Selector: uri}) //nolint:staticcheck // the selector field is the URI
	if err != nil {
		t.Fatalf("SyncFlags: %v", err)
	}
	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("the first message: %v", err)
	}
	if msg.GetFlagConfiguration() != flags {
		t.Errorf("the first message is %q, want the file's content", msg.GetFlagConfiguration())
	}
}

// TestFetchAllFlagsBySourceURI pins that FetchAllFlags takes the source URI from the selector field and returns the
// source's flag configuration, and that a selector that is no source URI fails the request with an unknown-code error.
func TestFetchAllFlagsBySourceURI(t *testing.T) {
	client := startProxy(t)
	uri, flags := writeFlags(t)

	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	resp, err := client.FetchAllFlags(ctx, &syncpb.FetchAllFlagsRequest{Selector: uri}) //nolint:staticcheck // the selector field is the URI
	if err != nil {
		t.Fatalf("FetchAllFlags: %v", err)
	}
	if resp.GetFlagConfiguration() != flags {
		t.Errorf("FetchAllFlags returned %q, want the file's content", resp.GetFlagConfiguration())
	}

	_, err = client.FetchAllFlags(ctx, &syncpb.FetchAllFlagsRequest{Selector: "flagSetId=set-a"}) //nolint:staticcheck // the selector field is the URI
	if err == nil {
		t.Fatal("FetchAllFlags answered for a selector expression")
	}
	if status.Code(err) != codes.Unknown {
		t.Errorf("the code is %v, want %v", status.Code(err), codes.Unknown)
	}
}
