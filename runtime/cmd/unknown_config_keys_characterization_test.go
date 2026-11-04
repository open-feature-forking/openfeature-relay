package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUnknownConfigKeysIgnored pins that keys of the configuration file which flagd-full does not know are ignored:
// the runtime.Config is the one the same file without them yields, and the command reaches the runtime. The settings
// of event sinks, middleware, endpoint settings and the event collector add such keys when they are delivered, which
// changes this (accepted.yaml).
func TestUnknownConfigKeysIgnored(t *testing.T) {
	const known = "port: 12013\nsources:\n  - uri: home.example:8015\n    provider: grpc\n"
	const unknown = `event-sinks:
  - type: webhook
    url: https://sink.example/events
middleware:
  - type: api-key
endpoint-settings:
  flag-evaluation:
    port: 1
event-collector:
  enabled: true
`
	without := runStartConfigCase(t, startConfigCase{name: "known keys", args: []string{"start"}, files: map[string]string{".agent.yaml": known}})
	with := runStartConfigCase(t, startConfigCase{name: "known and unknown keys", args: []string{"start"}, files: map[string]string{".agent.yaml": known + unknown}})

	require.Contains(t, without, "ServicePort: 12013")
	require.Equal(t, without, with)
}
