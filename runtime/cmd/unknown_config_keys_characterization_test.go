package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUnknownConfigKeysIgnored pins that keys of the configuration file which flagd-full does not know are ignored:
// the runtime.Config is the one the same file without them yields, and the command reaches the runtime. The event
// collector adds such a key when it is delivered, which changes this (accepted.yaml). The event-sinks, middleware and
// endpoint-settings keys no longer belong here: an entry in them names a component, and the distribution must contain
// it (TestInvalidExtensionSettingsStopStartup). Left empty they change nothing.
func TestUnknownConfigKeysIgnored(t *testing.T) {
	const known = "port: 12013\nsources:\n  - uri: home.example:8015\n    provider: grpc\n"
	const unknown = `event-sinks: []
middleware: []
endpoint-settings: {}
event-collector:
  enabled: true
`
	without := runStartConfigCase(t, startConfigCase{name: "known keys", args: []string{"start"}, files: map[string]string{".agent.yaml": known}})
	with := runStartConfigCase(t, startConfigCase{name: "known and unknown keys", args: []string{"start"}, files: map[string]string{".agent.yaml": known + unknown}})

	require.Contains(t, without, "ServicePort: 12013")
	require.Equal(t, without, with)
}
