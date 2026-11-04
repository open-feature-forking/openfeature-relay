package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is the control API. Every call runs under one lock: /start, /stop, /restart, /change and the delayed start of
// /restart never overlap, so a Load never meets a Start, a Stop or a rebuild of the target. The launchpad had two locks
// and let /change and /start interleave.
type Server struct {
	cfg    Config
	target Target
	log    *slog.Logger

	// mu serializes the calls. It is held while the target starts, which takes up to Config.StartTimeout.
	mu sync.Mutex
	// current is the configuration in use: the last one that started. restart cancels the pending delayed start. Both
	// are guarded by mu.
	current string
	restart context.CancelFunc

	// docMu guards the combined flag document, which the target reads while a call holds mu.
	docMu    sync.RWMutex
	combined map[string]any
	document []byte
	version  string

	addrMu sync.Mutex
	addr   string
}

// Option configures a Server.
type Option func(*Server)

// WithLogger sets the logger. The default is slog.Default().
func WithLogger(log *slog.Logger) Option { return func(s *Server) { s.log = log } }

// New returns the control server for the target. Serve reads the raw flags and starts the target.
func New(cfg Config, target Target, opts ...Option) *Server {
	s := &Server{cfg: cfg.WithDefaults(), target: target, log: slog.Default()}
	s.current = s.cfg.DefaultConfig
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Document returns the combined flag document and its version: the digest of its bytes. It is empty before Serve has
// read the raw flags. The target reads it when it loads the source that holds the combined flags; it never blocks on
// a call of the server.
func (s *Server) Document() (document []byte, version string) {
	s.docMu.RLock()
	defer s.docMu.RUnlock()
	return s.document, s.version
}

func (s *Server) setDocument(combined map[string]any, document []byte, version string) {
	s.docMu.Lock()
	defer s.docMu.Unlock()
	s.combined, s.document, s.version = combined, document, version
}

// Addr returns the address the API listens on, or "" before Serve is listening.
func (s *Server) Addr() string {
	s.addrMu.Lock()
	defer s.addrMu.Unlock()
	return s.addr
}

// Handler returns the control API without a listener of its own.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", s.handleStart)
	mux.HandleFunc("/stop", s.handleStop)
	mux.HandleFunc("/restart", s.handleRestart)
	mux.HandleFunc("/change", s.handleChange)
	return mux
}

// Serve combines the raw flags, starts the API on its own listener, which is never behind the target's gate, and
// starts the target with the default configuration. It returns when ctx ends, after it has removed the rendered file,
// or at once with the error of a failed combination, a failed listen, or a start of the default configuration that
// did not get ready.
func (s *Server) Serve(ctx context.Context) error {
	if err := s.load(); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", s.cfg.Address)
	if err != nil {
		return err
	}
	s.addrMu.Lock()
	s.addr = listener.Addr().String()
	s.addrMu.Unlock()

	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer func() {
		timeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(timeout) // no new call can schedule a restart after this
		s.cancelRestart()
		s.unrender()
	}()

	s.mu.Lock()
	err = s.start(ctx, s.cfg.DefaultConfig)
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("failed to start flagd: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// load combines the raw flags into the in-memory state. The source files are never written.
func (s *Server) load() error {
	combined, err := combine(s.cfg.RawFlags)
	if err != nil {
		return fmt.Errorf("error during initial JSON combination: %w", err)
	}
	document, version, err := encode(combined)
	if err != nil {
		return err
	}
	s.setDocument(combined, document, version)
	return nil
}

// response is the JSON body of every answer but the failures of /change.
type response struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func respond(w http.ResponseWriter, code int, status, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(response{Status: status, Message: message})
}

// handleStart starts the target with the configuration named by ?config=, or with the one in use.
func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	config := r.URL.Query().Get("config")

	s.mu.Lock()
	defer s.mu.Unlock()
	if config == "" {
		config = s.current
	}
	if err := s.start(context.WithoutCancel(r.Context()), config); err != nil {
		// A start that did not happen, or did not get ready, leaves a pending restart as it was: an unknown name must
		// not end the outage that the restart is going to end.
		respond(w, http.StatusInternalServerError, "error", fmt.Sprintf("Failed to start flagd: %v", err))
		return
	}
	s.cancelRestartLocked()
	respond(w, http.StatusOK, "success", "flagd started successfully")
}

// errNotReady is a start that returned without the target being ready.
var errNotReady = errors.New("flagd health check timed out")

// start runs the target's start for the configuration under the readiness timeout, renders the combined flags once the
// target is ready, and only then opens the target to its clients. A start that fails leaves the configuration in use as
// it was and the target closed. The caller holds mu.
func (s *Server) start(ctx context.Context, config string) error {
	if !s.known(config) {
		return fmt.Errorf("%w %q", ErrUnknownConfiguration, config)
	}

	startCtx, cancel := context.WithTimeout(ctx, s.cfg.StartTimeout)
	defer cancel()
	err := s.target.Start(startCtx, config)
	if err == nil && !s.target.Ready() {
		err = errNotReady
	}
	if errors.Is(err, ErrUnknownConfiguration) {
		return err // nothing was started, and nothing is stopped
	}
	if err == nil {
		document, _ := s.Document()
		if renderErr := s.render(document); renderErr != nil {
			s.log.Error("failed to render the combined flags", "error", renderErr)
		}
		err = s.target.Open(startCtx)
	}
	if err != nil {
		s.closeAfterFailedStart()
		if startCtx.Err() != nil {
			return errNotReady
		}
		return err
	}

	s.current = config
	return nil
}

// closeAfterFailedStart keeps the target closed, as the launchpad kills flagd when its health check times out.
func (s *Server) closeAfterFailedStart() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.target.Stop(ctx); err != nil {
		s.log.Error("failed to stop flagd after a start that failed", "error", err)
	}
}

// known reports whether the configuration exists. Without a configs directory the target decides.
func (s *Server) known(config string) bool {
	if s.cfg.Configs == "" {
		return true
	}
	if config == "" || strings.ContainsAny(config, `/\`) || strings.Contains(config, "..") {
		return false
	}
	info, err := os.Stat(filepath.Join(s.cfg.Configs, config+".json"))
	return err == nil && !info.IsDir()
}

// handleStop closes the target. A pending restart is cancelled, so the target stays closed until the next start.
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelRestartLocked()
	if err := s.target.Stop(context.WithoutCancel(r.Context())); err != nil {
		respond(w, http.StatusInternalServerError, "error", fmt.Sprintf("Failed to stop flagd: %v", err))
		return
	}
	respond(w, http.StatusOK, "success", "flagd stopped successfully")
}

// handleRestart closes the target, removes the rendered file, answers at once and starts the configuration in use
// after the delay. A later call cancels the pending start.
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	seconds := s.cfg.DefaultRestartSeconds
	if raw := r.URL.Query().Get("seconds"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			respond(w, http.StatusBadRequest, "error", "'seconds' must be a non-negative integer")
			return
		}
		seconds = n
	}

	s.mu.Lock()
	s.cancelRestartLocked()
	ctx, cancel := context.WithCancel(context.Background())
	s.restart = cancel
	s.unrender()
	if err := s.target.Stop(context.WithoutCancel(r.Context())); err != nil {
		s.log.Error("failed to restart flagd", "error", err)
	}
	s.mu.Unlock()

	go s.delayedStart(ctx, seconds)
	respond(w, http.StatusOK, "success", fmt.Sprintf("flagd will restart in %d seconds", seconds))
}

func (s *Server) delayedStart(ctx context.Context, seconds int) {
	s.log.Info("flagd will restart", "seconds", seconds)
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		s.log.Info("restart canceled before execution")
		return
	case <-timer.C:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil { // cancelled while this start waited for the lock
		s.log.Info("restart canceled before execution")
		return
	}
	s.restart = nil
	if err := s.start(context.Background(), s.current); err != nil {
		s.log.Error("failed to restart flagd", "error", err)
		return
	}
	s.log.Info("flagd restarted successfully")
}

func (s *Server) cancelRestart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelRestartLocked()
}

func (s *Server) cancelRestartLocked() {
	if s.restart != nil {
		s.restart()
		s.restart = nil
	}
}

// handleChange toggles the changing flag in the combined flags in memory, hands the new document to the target, and,
// once the target holds it, renders it. It works during an outage, with the target closed behind its gate, as the
// launchpad's file watcher did; and with no runtime to load into it fails at once. If the target does not take the
// document in, nothing is rendered; if the target refuses it, or has no runtime, the combined flags go back to what they
// were. A timeout is neither: the target may still take the document in late, so the combined flags stay as they are.
func (s *Server) handleChange(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	variant, err := s.change(context.WithoutCancel(r.Context()))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to toggle changing flag: %v", err), http.StatusInternalServerError)
		return
	}
	respond(w, http.StatusOK, "success", fmt.Sprintf("Default variant successfully changed to '%s'\n", variant))
}

func (s *Server) change(parent context.Context) (string, error) {
	s.docMu.RLock()
	previous, previousBytes, previousVersion := s.combined, s.document, s.version
	s.docMu.RUnlock()
	if previous == nil {
		return "", errors.New("the combined flags are not loaded")
	}

	next, err := cloneMap(previous)
	if err != nil {
		return "", err
	}
	variant, err := toggle(next, s.cfg.ChangingFlag, s.cfg.ChangingVariants)
	if err != nil {
		return "", err
	}
	document, version, err := encode(next)
	if err != nil {
		return "", err
	}

	// The target reads the new document through Document while it loads it, so it is set before the call.
	s.setDocument(next, document, version)
	ctx, cancel := context.WithTimeout(parent, s.cfg.ChangeTimeout)
	defer cancel()
	if err := s.target.Load(ctx, document, version); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return "", fmt.Errorf("Flags were not updated in time: %v", err) //nolint:staticcheck // the launchpad's text
		}
		s.setDocument(previous, previousBytes, previousVersion) // a definite refusal
		return "", err
	}

	if err := s.render(document); err != nil {
		return "", err
	}
	return variant, nil
}

// cloneMap copies the combined flags, so that a change the target rejects leaves the previous state untouched.
func cloneMap(m map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
