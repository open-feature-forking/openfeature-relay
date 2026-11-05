package flagdgrpc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
	"unicode"

	syncv1grpc "buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	v1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// retryOptedIn is true when either delay of the retry policy is set.
func (o Options) retryOptedIn() bool { return o.RetryBackoff != 0 || o.RetryBackoffMax != 0 }

// nextDelay is the delay before the next attempt to open a stream. Without the retry policy it is 4 s, 16 s, 64 s and
// then 60 s, counted by iteration; with it, a fixed retryBackoffMaxMs.
func (g *Sync) nextDelay(iteration *int) time.Duration {
	if g.Options.retryOptedIn() {
		if g.Options.RetryBackoffMax != 0 {
			return g.Options.RetryBackoffMax
		}
		return defaultRetryBackoffMax
	}
	if *iteration >= backOffLimit {
		return constantBackOffDelay * time.Second
	}
	*iteration++
	return time.Duration(math.Pow(backOffBase, float64(*iteration))) * time.Second
}

// openStream calls SyncFlags. The call waits for the connection exactly when the retry policy is opted in; otherwise
// it fails at once on an unreachable server.
func (g *Sync) openStream(ctx context.Context) (syncv1grpc.FlagSyncService_SyncFlagsClient, error) {
	var callOptions []grpc.CallOption
	if g.Options.retryOptedIn() {
		callOptions = append(callOptions, grpc.WaitForReady(true))
	}
	return g.client.SyncFlags(g.contextWithHeaders(ctx), &v1.SyncFlagsRequest{ProviderId: g.ProviderID, Selector: g.Selector}, callOptions...) //nolint:staticcheck // deprecated selector kept for backwards compat
}

// streamContext bounds a stream by the stream deadline, if there is one. The returned function releases the context.
func (g *Sync) streamContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if g.Options.StreamDeadline > 0 {
		return context.WithTimeout(ctx, g.Options.StreamDeadline)
	}
	return ctx, func() {}
}

// deadlineElapsed tells a stream that ended at its deadline, with the parent context alive, from one that failed.
func (g *Sync) deadlineElapsed(parent, stream context.Context) bool {
	return g.Options.StreamDeadline > 0 && parent.Err() == nil && errors.Is(stream.Err(), context.DeadlineExceeded)
}

func (g *Sync) logStreamEnd(err error, recycled bool) {
	if recycled {
		g.Logger.Debug(fmt.Sprintf("stream to %s reached its deadline, opening it again", g.URI))
		return
	}
	g.Logger.Warn(fmt.Sprintf("error with stream listener: %s", err.Error()))
}

// fatalError is non-nil when err carries one of the fatal status codes before the first payload of the source; Sync
// returns it and the runtime ends. After the first payload the code is retried like any error. A shutdown, or a stream
// that ended at its deadline, is no failure of the source, however grpc-go maps its context error.
func (g *Sync) fatalError(ctx context.Context, err error, recycled bool) error {
	if len(g.Options.FatalStatusCodes) == 0 || g.firstPayload.Load() || ctx.Err() != nil || recycled {
		return nil
	}
	code := status.Code(err)
	if !slices.Contains(g.Options.FatalStatusCodes, code) {
		return nil
	}
	return fmt.Errorf("sync source %q: status %s before its first payload: %w", g.URI, statusName(code), err)
}

// statusName is the name of a gRPC status code as the operator writes it in fatalStatusCodes: PERMISSION_DENIED, not
// the PermissionDenied of Code.String.
func statusName(code codes.Code) string {
	if code == codes.Canceled { // spelled with two l in the status code names
		return "CANCELLED"
	}
	var name []rune
	for i, r := range code.String() {
		if i > 0 && unicode.IsUpper(r) {
			name = append(name, '_')
		}
		name = append(name, unicode.ToUpper(r))
	}
	return string(name)
}
