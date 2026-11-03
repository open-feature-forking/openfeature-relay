package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	flagdservice "github.com/open-feature/flagd/flagd/pkg/service"
)

// managementShutdownBudget is how long the management port gets to finish its requests once the runtime stops.
const managementShutdownBudget = 5 * time.Second

// serveManagement serves the management port, health, readiness and metrics, until ctx ends. Readiness is the
// runtime's own: every source accepted and every sync provider ready, and not while the runtime shuts down.
func (r *Runtime) serveManagement(ctx context.Context) error {
	r.Logger.Info(fmt.Sprintf("metrics and probes listening at %d", r.ServiceConfig.ManagementPort))
	server := flagdservice.NewManagementServer(r.ServiceConfig.ManagementPort, func() bool {
		return !r.shuttingDown.Load() && r.isReady()
	}, r.MetricsHandler)

	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServe() }()

	select {
	case err := <-errs:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		r.shuttingDown.Store(true)
		shutdown, cancel := context.WithTimeout(context.Background(), managementShutdownBudget)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return fmt.Errorf("error shutting down the management port: %w", err)
		}
		<-errs
		return nil
	}
}
