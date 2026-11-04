package sync

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// shutdownTimeout bounds the graceful drain of in-flight non-streaming requests.
const shutdownTimeout = 5 * time.Second

type ISyncService interface {
	// Start the sync service
	Start(context.Context) error

	// Emit updates for sync listeners
	Emit(source string)
}

type SvcConfigurations struct {
	Logger              *logger.Logger
	Port                uint16
	Sources             []string
	Store               store.IStore
	ContextValues       map[string]any
	CertPath            string
	KeyPath             string
	SocketPath          string
	StreamDeadline      time.Duration
	DisableSyncMetadata bool
	Telemetry           telemetry.Provider

	HTTPEnabled           bool
	ServiceName           string
	CORS                  []string
	Options               []connect.HandlerOption
	MaxRequestHeaderBytes int64
	MaxRequestBodyBytes   int64

	// HTTPMiddleware wraps the routes after CORS and before their instrumentation. Nil means none.
	HTTPMiddleware func(http.Handler) http.Handler
	// Listen opens the service's listener; nil means net.Listen.
	Listen service.ListenFunc
	// Initialized, when set, ends the wait for the first delivery of every source once it is closed.
	Initialized <-chan struct{}
	// Formats are the configuration formats of the distribution. A request may name one other than flagd's in the
	// FormatHeader and gets its one document.
	Formats []string
}

type Service struct {
	listener   net.Listener
	endStreams context.CancelFunc
	logger     *logger.Logger
	server     *http.Server
	modTime    *modTime
	changes    *changeSignal

	startupTracker syncTracker
	initialized    <-chan struct{}
}

func loadTLSConfig(certPath string, keyPath string) (*tls.Config, error) {
	serverCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load key pair from certificate paths '%s' and '%s': %w", certPath, keyPath, err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.NoClientCert,
		MinVersion:   tls.VersionTLS12,
		// h2 first so gRPC clients get HTTP/2; http/1.1 keeps the other surfaces reachable
		NextProtos: []string{"h2", "http/1.1"},
	}, nil
}

func NewSyncService(cfg SvcConfigurations) (*Service, error) {
	var err error
	l := cfg.Logger

	cfg.Telemetry = telemetry.OrNoop(cfg.Telemetry)

	stamp := &modTime{}
	changes := newChangeSignal()

	// nil leaves the routes unregistered
	var flagsHandler http.Handler
	if cfg.HTTPEnabled {
		flagsHandler = httpHandler{store: cfg.Store, log: l, modTime: stamp, formats: cfg.Formats}
	}

	// the requests end with this context, which is cancelled when the service shuts down: an open SyncFlags stream
	// would otherwise hold the graceful shutdown for its whole limit
	streams, endStreams := context.WithCancel(context.Background())
	server := newConnectServer(cfg, syncHandler{
		store:               cfg.Store,
		log:                 l,
		contextValues:       cfg.ContextValues,
		deadline:            cfg.StreamDeadline,
		disableSyncMetadata: cfg.DisableSyncMetadata,
		metricsRecorder:     flagdService.NewMetrics(cfg.Telemetry.Meter(flagdService.MetricsScope)),
		formats:             cfg.Formats,
		changes:             changes,
	}, flagsHandler)

	server.BaseContext = func(net.Listener) context.Context { return streams }

	// Loaded here rather than in ServeTLS so a bad path fails at construction.
	if cfg.CertPath != "" && cfg.KeyPath != "" {
		tlsConfig, tlsErr := loadTLSConfig(cfg.CertPath, cfg.KeyPath)
		if tlsErr != nil {
			endStreams()
			return nil, fmt.Errorf("failed to load TLS cert and key: %w", tlsErr)
		}
		server.TLSConfig = tlsConfig
	}

	var lis net.Listener
	if cfg.SocketPath != "" {
		l.Info(fmt.Sprintf("starting flag sync service at %s", cfg.SocketPath))
		lis, err = cfg.Listen.Or()("unix", cfg.SocketPath)
	} else {
		l.Info(fmt.Sprintf("starting flag sync service on port %d", cfg.Port))
		lis, err = cfg.Listen.Or()("tcp", fmt.Sprintf(":%d", cfg.Port))
	}
	if err != nil {
		endStreams()
		return nil, fmt.Errorf("error creating listener: %w", err)
	}

	return &Service{
		listener:    lis,
		endStreams:  endStreams,
		logger:      l,
		server:      server,
		modTime:     stamp,
		changes:     changes,
		initialized: cfg.Initialized,
		startupTracker: syncTracker{
			sources:  slices.Clone(cfg.Sources),
			doneChan: make(chan interface{}),
		},
	}, nil
}

func (s *Service) Start(ctx context.Context) error {
	// derive errgroup so we track ctx for exit as well as startup errors
	g, lCtx := errgroup.WithContext(ctx)

	ready := make(chan struct{})
	g.Go(func() error {
		s.waitForInitialSync()
		close(ready)
		return nil
	})

	g.Go(func() error {
		<-ready

		err := s.serve()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Warn(fmt.Sprintf("error from sync server start: %v", err))
		}
		return nil
	})

	g.Go(func() error {
		<-lCtx.Done()
		s.shutdown()

		return nil
	})

	err := g.Wait()
	if err != nil {
		return fmt.Errorf("error from sync service: %w", err)
	}

	return nil
}

// serve leaves the ServeTLS paths empty because the certificates are already in TLSConfig.
func (s *Service) serve() error {
	if s.server.TLSConfig != nil {
		return s.server.ServeTLS(s.listener, "", "")
	}
	return s.server.Serve(s.listener)
}

func (s *Service) Emit(source string) {
	s.startupTracker.trackAndRemove(source)
	// Stamped here rather than from a store watch, which would re-marshal just to date it. This can
	// run ahead of a real content change; the ETag stays exact.
	s.modTime.set(time.Now())
	if s.changes != nil {
		s.changes.broadcast()
	}
}

// waitForInitialSync delays serving until every known sync source has reported its first sync, so
// clients cannot be handed a partial configuration at startup.
func (s *Service) waitForInitialSync() {
	select {
	case <-time.After(5 * time.Second):
		s.logger.Warn("timeout while waiting for all sync sources to complete their initial sync. " +
			"continuing sync service")
	case <-s.startupTracker.getDone():
	case <-s.initialized:
		// every source delivered; the first delivery dates the flags
		if s.modTime.get().IsZero() {
			s.modTime.set(time.Now())
		}
	}
}

func (s *Service) shutdown() {
	s.logger.Info("shutting down flag sync service")
	s.endStreams()

	// Bounded: a long-lived SyncFlags stream would otherwise hold the graceful shutdown open
	// indefinitely, while unary requests get the window to finish.
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		s.logger.Warn("error from sync server shutdown", zap.Error(err))
		if err := s.server.Close(); err != nil {
			s.logger.Warn("error from sync server close", zap.Error(err))
		}
	}

	// Serve may never have started, in which case http.Server does not know the listener.
	if err := s.listener.Close(); err != nil {
		s.logger.Debug("error closing sync service listener", zap.Error(err))
	}
}

// syncTracker is a helper to track sync payloads at the startup
// It simply starts with known set of sync sources and remove
type syncTracker struct {
	sources  []string
	doneChan chan interface{}
}

func (t *syncTracker) getDone() <-chan interface{} {
	return t.doneChan
}

// trackAndRemove tracks sources and remove channel if all sources that are tracking are complete.
func (t *syncTracker) trackAndRemove(source string) {
	index := slices.Index(t.sources, source)
	if index < 0 {
		return
	}

	t.sources = slices.Delete(t.sources, index, index+1)

	if len(t.sources) == 0 {
		close(t.doneChan)
	}
}
