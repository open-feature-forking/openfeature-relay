package concurrency

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestStreamCloseEvents: client.close() on an open sync stream fires error ("canceled by client") and then end. load.js
// keeps a planned close out of stream_error on that; run it again when k6 is upgraded.
func TestStreamCloseEvents(t *testing.T) {
	requireLoad(t)
	p := startFlagd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run := runK6(ctx, t, p, "close.js", nil)
	require.Zero(t, run.ExitCode, run.Output)
	require.Contains(t, run.Output, `"payloads":1`)
	line := run.Output[strings.Index(run.Output, "CLOSE "):]
	require.Regexp(t, `"events":\["close","error:canceled by client[^"]*","end"\]`, strings.SplitN(line, "\n", 2)[0])
}
