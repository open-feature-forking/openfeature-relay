package cmd

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/testbed/control"
	"github.com/open-feature-forking/openfeature-relay/testbed/source"
	"go.uber.org/zap"
)

// ConfigFromEnv fills the control module's configuration from TESTBED_* environment variables. An unset variable
// leaves the default, which reproduces flagd-testbed v3.11.0.
func ConfigFromEnv(lookup func(string) (string, bool)) control.Config {
	var cfg control.Config
	str := func(key string, into *string) {
		if v, ok := lookup(key); ok && v != "" {
			*into = v
		}
	}
	str("TESTBED_ADDRESS", &cfg.Address)
	str("TESTBED_CONFIGS", &cfg.Configs)
	str("TESTBED_RAW_FLAGS", &cfg.RawFlags)
	str("TESTBED_RENDER", &cfg.Render)
	str("TESTBED_DEFAULT_CONFIG", &cfg.DefaultConfig)
	dur := func(key string, into *time.Duration) {
		if v, ok := lookup(key); ok {
			if d, err := time.ParseDuration(v); err == nil {
				*into = d
			}
		}
	}
	dur("TESTBED_START_TIMEOUT", &cfg.StartTimeout)
	dur("TESTBED_CHANGE_TIMEOUT", &cfg.ChangeTimeout)
	if v, ok := lookup("TESTBED_DEFAULT_RESTART_SECONDS"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.DefaultRestartSeconds = n
		}
	}
	return cfg
}

// Execute runs the testbed: the control API on its own listener, and the flagd runtime of the components behind a gate.
// The generated main calls it. It returns when the process is told to stop.
func Execute(info distribution.Info, components distribution.Components, _ distribution.Inventory) {
	zl, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("can't initialize zap logger: %v", err)
	}
	cfg := ConfigFromEnv(os.LookupEnv).WithDefaults()
	t := newTarget(cfg.Configs, info.Version, source.Default, components, logger.NewLogger(zl, false))
	server := control.New(cfg, t)
	t.document = server.Document

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = server.Serve(ctx)
	t.shutdown()
	if err != nil {
		log.Fatal(err)
	}
}
