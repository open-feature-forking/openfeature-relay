package sse

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/launchdarkly/eventsource"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
)

// quiet is how long a stream is watched to count its events.
const quiet = 500 * time.Millisecond

// subscribed starts a service over the store, following the bus, and returns a subscription on channel.
func subscribed(t *testing.T, s *store.Store, bus *events.Bus, channel string) *eventsource.Stream {
	t.Helper()
	svc := New(s, Config{Logger: logger.NewLogger(nil, false), HeartbeatInterval: time.Hour, Bus: bus, Formats: testFormats})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = svc.Start(ctx) }()

	stream, err := eventsource.SubscribeWithURL(streamURL(newStreamServer(t, svc), channel))
	require.NoError(t, err)
	t.Cleanup(stream.Close)
	time.Sleep(100 * time.Millisecond) // the stream registers after the 200 is written
	return stream
}

// drain returns the events that arrive within quiet.
func drain(stream *eventsource.Stream) []eventsource.Event {
	var got []eventsource.Event
	deadline := time.After(quiet)
	for {
		select {
		case ev := <-stream.Events:
			got = append(got, ev)
		case <-deadline:
			return got
		}
	}
}

// TestRefetchEvents pins the refetch event stream of the OFREP endpoint: the payload, one event per change of a
// document behind the selector, none for an identical delivery or a change to another selector, and one for a
// change of a vendor-format document.
func TestRefetchEvents(t *testing.T) {
	s, err := store.NewStore(logger.NewLogger(nil, false), []string{"src1", "src2", "vendor"})
	require.NoError(t, err)
	bus := events.NewBus()
	deliver(t, s, bus, "flagd", "src1", "fs1", "v1", "a")
	deliver(t, s, bus, "flagd", "src2", "fs2", "v1", "b")

	stream := subscribed(t, s, bus, "flagSetId=fs1")

	// a change to the selected flag set: one event
	deliver(t, s, bus, "flagd", "src1", "fs1", "v2", "a")
	got := drain(stream)
	require.Len(t, got, 1)
	require.Equal(t, "message", got[0].Event())
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(got[0].Data()), &payload))
	require.Equal(t, "refetchEvaluation", payload["type"])
	require.NotEmpty(t, payload["etag"], "the config version token")
	require.NotZero(t, payload["lastModified"], "the time of the change, in Unix seconds")
	require.Len(t, payload, 3, "type, etag and lastModified")

	// the same version again: none
	deliver(t, s, bus, "flagd", "src1", "fs1", "v2", "a")
	require.Empty(t, drain(stream))

	// a change to another flag set: none
	deliver(t, s, bus, "flagd", "src2", "fs2", "v2", "b")
	require.Empty(t, drain(stream))

	// a vendor-format document of the flag set: one
	deliver(t, s, bus, "vendor", "vendor", "fs1", "v1", "k")
	require.Len(t, drain(stream), 1)

	// and the next flagd-format change is announced as before
	deliver(t, s, bus, "flagd", "src1", "fs1", "v3", "a")
	require.Len(t, drain(stream), 1)
}

// TestEventStreamVendorFormat pins that a vendor format's delivery reaches the stream, though no flagd flag
// changes, and that the version token the event carries is the one a conditional request is compared with.
func TestEventStreamVendorFormat(t *testing.T) {
	s, err := store.NewStore(logger.NewLogger(nil, false), []string{"src1", "vendor"})
	require.NoError(t, err)
	bus := events.NewBus()
	svc := New(s, Config{Logger: logger.NewLogger(nil, false), HeartbeatInterval: time.Hour, Bus: bus, Formats: testFormats})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = svc.Start(ctx) }()

	stream, err := eventsource.SubscribeWithURL(streamURL(newStreamServer(t, svc), ""))
	require.NoError(t, err)
	t.Cleanup(stream.Close)
	time.Sleep(100 * time.Millisecond)
	before, _, ok := svc.Tracker().Version("")
	require.True(t, ok)

	deliver(t, s, bus, "vendor", "vendor", "", "v1", "segment")

	got := drain(stream)
	require.Len(t, got, 1)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(got[0].Data()), &payload))
	after, _, _ := svc.Tracker().Version("")
	require.NotEqual(t, before, after)
	require.Equal(t, after, payload["etag"])
}
