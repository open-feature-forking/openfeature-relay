//go:build unix

package runtime

import (
	"context"
	"fmt"
	"github.com/open-feature-forking/openfeature-relay/runtime/internal/officialset"
	"net"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/stretchr/testify/require"
)

// TestPortInUse pins which step fails, and with which error, when a port an endpoint listens on is taken. The
// management port is the runtime's own: its error no longer comes through the flag evaluation service.
func TestPortInUse(t *testing.T) {
	tests := []struct {
		name string
		port func(*Config) *uint16
		// fromConfig is the error text of FromConfig; otherwise start fails with start
		fromConfig string
		start      string
	}{
		{
			name:  "management port",
			port:  func(c *Config) *uint16 { return &c.ManagementPort },
			start: "error from the management port: listen tcp :%d: bind: address already in use",
		},
		{
			name:  "flag evaluation port",
			port:  func(c *Config) *uint16 { return &c.ServicePort },
			start: "error from service endpoint \"flag-evaluation\": error creating listener for flag evaluation service: listen tcp :%d: bind: address already in use",
		},
		{
			name:  "OFREP port",
			port:  func(c *Config) *uint16 { return &c.OfrepServicePort },
			start: "error from service endpoint \"ofrep\": error from ofrep service: error from ofrep service: listen tcp :%d: bind: address already in use",
		},
		{
			name:       "flag sync port",
			port:       func(c *Config) *uint16 { return &c.SyncServicePort },
			fromConfig: "error creating the service endpoint \"flag-sync\": error creating sync service: error creating listener: listen tcp :%d: bind: address already in use",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := characterizedConfig(t, newPorts(t))
			busy := tt.port(&cfg)
			l, err := net.Listen("tcp", fmt.Sprintf(":%d", *busy))
			require.NoError(t, err)
			defer l.Close()

			rt, err := FromConfig(logger.NewLogger(nil, false), "test", cfg, officialset.Components())
			if tt.fromConfig != "" {
				require.EqualError(t, err, fmt.Sprintf(tt.fromConfig, *busy))
				return
			}
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- rt.start(ctx) }()
			select {
			case err := <-result:
				require.EqualError(t, err, "errgroup closed with error: "+fmt.Sprintf(tt.start, *busy))
			case <-time.After(10 * time.Second):
				t.Fatal("start keeps running with a port taken")
			}
		})
	}
}
