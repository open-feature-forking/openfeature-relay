package flagdgrpc

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	v1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	grpccredential "github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc/credentials"
	_ "github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc/nameresolvers" // initialize custom resolvers e.g. envoy.Init()
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

const (
	// Prefix for GRPC URL inputs. GRPC does not define a standard prefix. This prefix helps to differentiate remote
	// URLs for REST APIs (i.e - HTTP) from GRPC endpoints.
	Prefix          = "grpc://"
	PrefixSecure    = "grpcs://"
	SupportedScheme = "(envoy|dns|uds|xds)"

	// Connection retry constants
	// Back off period is calculated with backOffBase ^ #retry-iteration. However, when #retry-iteration count reach
	// backOffLimit, retry delay fallback to constantBackOffDelay
	backOffLimit         = 3
	backOffBase          = 4
	constantBackOffDelay = 60
)

// type aliases for interfaces required by this component - needed for mock generation with gomock

type FlagSyncServiceClient interface {
	syncv1grpc.FlagSyncServiceClient
}
type FlagSyncServiceClientResponse interface {
	syncv1grpc.FlagSyncService_SyncFlagsClient
}

type Sync struct {
	GrpcDialOptionsOverride []grpc.DialOption
	CertPath                string
	CredentialBuilder       grpccredential.Builder
	Logger                  *logger.Logger
	ProviderID              string
	Secure                  bool
	Selector                string
	URI                     string
	MaxMsgSize              int
	IncrementalUpdates      bool
	Headers                 map[string]string

	// Options are the opt-in settings of the source (the `grpc` object); the zero value is today's behaviour.
	Options Options

	// SyncMetricsRecorder, when non-nil, is used to record the source-agnostic
	// client-side sync metrics (feature_flag.flagd.sync.client.flag_config.*). Nil is
	// safe — the recorder's methods tolerate nil receivers.
	SyncMetricsRecorder sync.MetricsRecorder

	// StreamMetricsRecorder, when non-nil, records the lifecycle of the sync stream. Nil records nothing.
	StreamMetricsRecorder StreamMetricsRecorder

	// DialOptions are added to the client connection after the credentials, in both the default and
	// the override path. A distribution passes its client instrumentation (a stats handler) here.
	DialOptions []grpc.DialOption

	client FlagSyncServiceClient
	conn   *grpc.ClientConn // closed when Sync returns
	ready  atomic.Bool
	// firstPayload is set when the first payload of the source arrived, and tells a fatal status code from a failure
	// after the source was up. ready is set when the stream opens, so it cannot.
	firstPayload atomic.Bool

	// after returns the channel that fires once the delay is over; time.After when nil. Tests replace it.
	after func(time.Duration) <-chan time.Time
	// dialRecorder, when non-nil, receives the options the client connection is created with. Tests set it.
	dialRecorder func([]grpc.DialOption)

	streamOpen  atomic.Bool
	lastContact atomic.Int64 // unix nanoseconds at which the last stream ended; zero before the first
}

var _ sync.ContactReporter = (*Sync)(nil)

func (g *Sync) Init(_ context.Context) error {
	var rpcCon *grpc.ClientConn // Reusable client connection
	var err error

	if len(g.GrpcDialOptionsOverride) > 0 {
		g.Logger.Debug("GRPC DialOptions override provided")
		opts := make([]grpc.DialOption, 0, len(g.GrpcDialOptionsOverride)+len(g.DialOptions))
		opts = append(opts, g.GrpcDialOptionsOverride...)
		opts = append(opts, g.DialOptions...)
		g.recordDial(opts)
		rpcCon, err = grpc.NewClient(g.URI, opts...)
	} else {
		var tCredentials credentials.TransportCredentials
		tCredentials, err = g.CredentialBuilder.Build(g.Secure, g.CertPath)
		if err != nil {
			err = fmt.Errorf("error building transport credentials: %w", err)
			g.Logger.Error(err.Error())
			return err
		}

		opts := []grpc.DialOption{grpc.WithTransportCredentials(tCredentials)}
		// Set MaxMsgSize if passed
		if g.MaxMsgSize > 0 {
			g.Logger.Info(fmt.Sprintf("setting max receive message size %d bytes default 4MB", g.MaxMsgSize))
			opts = append(opts, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(g.MaxMsgSize)))
		}
		opts = append(opts, g.DialOptions...)
		g.recordDial(opts)
		rpcCon, err = grpc.NewClient(g.URI, opts...)
	}

	if err != nil {
		err := fmt.Errorf("error initiating grpc client connection: %w", err)
		g.Logger.Error(err.Error())
		return err
	}

	// Setup service client
	g.conn = rpcCon
	g.client = syncv1grpc.NewFlagSyncServiceClient(rpcCon)

	return nil
}

func (g *Sync) recordDial(opts []grpc.DialOption) {
	if g.dialRecorder != nil {
		g.dialRecorder(opts)
	}
}

func (g *Sync) contextWithHeaders(ctx context.Context) context.Context {
	if len(g.Headers) == 0 {
		return ctx
	}
	pairs := make([]string, 0, len(g.Headers)*2)
	for k, v := range g.Headers {
		pairs = append(pairs, k, v)
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...)
}

func (g *Sync) ReSync(ctx context.Context, dataSync chan<- sync.DataSync) error {
	res, err := g.client.FetchAllFlags(g.contextWithHeaders(ctx), &v1.FetchAllFlagsRequest{ProviderId: g.ProviderID, Selector: g.Selector}) //nolint:staticcheck // deprecated selector kept for backwards compat
	if err != nil {
		err = fmt.Errorf("error fetching all flags: %w", err)
		g.Logger.Error(err.Error())
		return err
	}
	update := sync.DataSync{
		FlagData:           res.GetFlagConfiguration(),
		Source:             g.URI,
		IncrementalUpdates: g.IncrementalUpdates,
	}
	select {
	case dataSync <- update:
		sync.RecordFlagConfigReceived(ctx, g.SyncMetricsRecorder, sync.SourceGRPC, g.URI, g.Selector)
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (g *Sync) IsReady() bool {
	return g.ready.Load()
}

// LastContact is the current time while a stream is open, else when the last stream ended; the zero time before the
// first stream.
func (g *Sync) LastContact() time.Time {
	if g.streamOpen.Load() {
		return time.Now()
	}
	if n := g.lastContact.Load(); n != 0 {
		return time.Unix(0, n)
	}
	return time.Time{}
}

// closeConn closes the client connection, once.
func (g *Sync) closeConn() {
	if conn := g.conn; conn != nil {
		g.conn = nil
		_ = conn.Close()
	}
}

func (g *Sync) Sync(ctx context.Context, dataSync chan<- sync.DataSync) error {
	defer g.closeConn()
	g.Logger.Info(fmt.Sprintf("starting sync from %s", g.URI))

	// Initialize SyncFlags client. A failure to open the stream, such as an unreachable server, is retried with a
	// backoff like the loss of a stream that was open
	g.Logger.Debug(fmt.Sprintf("initial stream connection to %s", g.URI))
	streamCtx, cancel := g.streamContext(ctx)
	syncClient, err := g.openStream(streamCtx)
	recycled := false
	if err != nil {
		recycled = g.deadlineElapsed(ctx, streamCtx)
		cancel()
		if fatal := g.fatalError(ctx, err, recycled); fatal != nil {
			return fatal
		}
		g.Logger.Warn(fmt.Sprintf("unable to sync flags: %s", err.Error()))
	} else {
		g.streamOpened(ctx)

		g.Logger.Debug(fmt.Sprintf("watching %s for changes", g.URI))

		// Initial stream listening. Error will be logged and continue and retry connection establishment
		err = g.handleFlagSync(ctx, syncClient, dataSync, false)
		g.streamClosed(ctx)
		recycled = g.deadlineElapsed(ctx, streamCtx)
		cancel()
		if err == nil {
			// This should not happen as handleFlagSync expects to return with an error
			return nil
		}
		if fatal := g.fatalError(ctx, err, recycled); fatal != nil {
			return fatal
		}
		g.logStreamEnd(err, recycled)
	}

	// retry connection establishment
	for {
		opened, fatal := g.reconnect(ctx, recycled)
		if fatal != nil {
			return fatal
		}
		if opened == nil {
			// We shall exit
			return nil
		}
		g.streamOpened(ctx)

		err = g.handleFlagSync(ctx, opened.client, dataSync, !recycled)
		g.streamClosed(ctx)
		recycled = g.deadlineElapsed(ctx, opened.ctx)
		opened.cancel()
		if err != nil {
			if fatal := g.fatalError(ctx, err, recycled); fatal != nil {
				return fatal
			}
			g.logStreamEnd(err, recycled)
			continue
		}
	}
}

func (g *Sync) afterFunc() func(time.Duration) <-chan time.Time {
	if g.after != nil {
		return g.after
	}
	return time.After
}

// connectWithRetry is a helper that performs exponential back off after retrying connection attempts periodically until
// a successful connection is established. Caller must not expect an error. Hence, errors are handled, logged
// internally. However, if the provided context is done, method exit with a non-ok state which must be verified by the
// caller
func (g *Sync) connectWithRetry(
	ctx context.Context,
) (syncv1grpc.FlagSyncService_SyncFlagsClient, bool) {
	opened, _ := g.reconnect(ctx, false)
	if opened == nil {
		return nil, false
	}
	return opened.client, true
}

// openedStream is a SyncFlags stream with the context that bounds it and the function that releases that context.
type openedStream struct {
	client syncv1grpc.FlagSyncService_SyncFlagsClient
	ctx    context.Context
	cancel context.CancelFunc
}

// reconnect opens a stream again, waiting the delay of the schedule before each attempt. After a stream that ended at
// its deadline (immediate), the first attempt follows at once. It returns nil, nil when ctx is done, and an error when
// a fatal status code ends the source.
func (g *Sync) reconnect(ctx context.Context, immediate bool) (*openedStream, error) {
	var iteration int

	for {
		sleep := time.Duration(0)
		if !immediate {
			sleep = g.nextDelay(&iteration)
		}
		immediate = false

		// Block the next connection attempt and check the context
		if sleep > 0 {
			select {
			case <-g.afterFunc()(sleep):
			case <-ctx.Done():
				// context done means we shall exit
				return nil, nil
			}
		} else if ctx.Err() != nil {
			return nil, nil
		}

		g.Logger.Warn(fmt.Sprintf("connection re-establishment attempt in-progress for grpc target: %s", g.URI))

		streamCtx, cancel := g.streamContext(ctx)
		syncClient, err := g.openStream(streamCtx)
		if err != nil {
			recycled := g.deadlineElapsed(ctx, streamCtx)
			cancel()
			if fatal := g.fatalError(ctx, err, recycled); fatal != nil {
				return nil, fatal
			}
			g.Logger.Debug(fmt.Sprintf("error opening service client: %s", err.Error()))
			continue
		}

		g.Logger.Info(fmt.Sprintf("connection re-established with grpc target: %s", g.URI))
		return &openedStream{client: syncClient, ctx: streamCtx, cancel: cancel}, nil
	}
}

// handleFlagSync wraps the stream listening and push updates through dataSync channel
func (g *Sync) handleFlagSync(
	ctx context.Context,
	stream syncv1grpc.FlagSyncService_SyncFlagsClient,
	dataSync chan<- sync.DataSync,
	isReconnect bool,
) error {
	if !g.Options.ReadyOnFirstPayload {
		g.ready.Store(true)
	}
	g.streamOpen.Store(true)
	defer func() {
		g.lastContact.Store(time.Now().UnixNano())
		g.streamOpen.Store(false)
	}()

	firstRecv := true
	for {
		data, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("error receiving payload from stream: %w", err)
		}
		g.firstPayload.Store(true)
		if g.Options.ReadyOnFirstPayload {
			g.ready.Store(true)
		}

		// only count a reconnect once the stream is confirmed; a rejected stream errors above
		if firstRecv {
			firstRecv = false
			if isReconnect {
				g.streamReconnected(ctx)
			}
		}

		update := sync.DataSync{
			FlagData:           data.FlagConfiguration,
			SyncContext:        data.SyncContext,
			Source:             g.URI,
			Selector:           g.Selector,
			IncrementalUpdates: g.IncrementalUpdates,
		}
		select {
		case dataSync <- update:
			sync.RecordFlagConfigReceived(ctx, g.SyncMetricsRecorder, sync.SourceGRPC, g.URI, g.Selector)
		case <-ctx.Done():
			return ctx.Err()
		}

		g.Logger.Debug("received full configuration payload")
	}
}
