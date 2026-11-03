package sse

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/launchdarkly/eventsource"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
)

// quiet is how long a stream is watched to count its events.
const quiet = 500 * time.Millisecond

// subscribed starts a service over the store and returns a subscription on channel.
func subscribed(t *testing.T, s *store.Store, channel string) *eventsource.Stream {
	t.Helper()
	svc := New(s, Config{Logger: logger.NewLogger(nil, false), HeartbeatInterval: time.Hour})
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
	var events []eventsource.Event
	deadline := time.After(quiet)
	for {
		select {
		case ev := <-stream.Events:
			events = append(events, ev)
		case <-deadline:
			return events
		}
	}
}

// TestRefetchEventsToday pins the refetch event stream of the OFREP endpoint: the payload, one event per
// change, none for an identical update or a change to another selector, and none for a change of a
// vendor-format document. The vendor case is flipped when change announcements come from document versions.
func TestRefetchEventsToday(t *testing.T) {
	log := logger.NewLogger(nil, false)
	s, err := store.NewStore(log, []string{"src1", "src2", "vendor"})
	require.NoError(t, err)
	s.Update("src1", []model.Flag{testFlag("fs1", "a", "on")}, model.Metadata{"flagSetId": "fs1"}, false)
	s.Update("src2", []model.Flag{testFlag("fs2", "b", "on")}, model.Metadata{"flagSetId": "fs2"}, false)

	stream := subscribed(t, s, "flagSetId=fs1")

	// a change to the selected flag set: one event
	s.Update("src1", []model.Flag{testFlag("fs1", "a", "off")}, model.Metadata{"flagSetId": "fs1"}, false)
	events := drain(stream)
	require.Len(t, events, 1)
	require.Equal(t, "message", events[0].Event())
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(events[0].Data()), &payload))
	require.Equal(t, "refetchEvaluation", payload["type"])
	require.NotEmpty(t, payload["etag"], "the config version token")
	require.NotZero(t, payload["lastModified"], "the time of the change, in Unix seconds")
	require.Len(t, payload, 3, "type, etag and lastModified")

	// the same flags again: none
	s.Update("src1", []model.Flag{testFlag("fs1", "a", "off")}, model.Metadata{"flagSetId": "fs1"}, false)
	require.Empty(t, drain(stream))

	// a change to another flag set: none
	s.Update("src2", []model.Flag{testFlag("fs2", "b", "off")}, model.Metadata{"flagSetId": "fs2"}, false)
	require.Empty(t, drain(stream))

	// a vendor-format document, as a format other than flagd's writes it: none
	require.NoError(t, s.Apply(store.ApplyRequest{
		Format: "vendor", Source: "vendor",
		Documents: []store.Document{{Format: "vendor", Source: "vendor", FlagSet: "fs1", Version: "v1", FlagKeys: []string{"k"}}},
	}))
	require.Empty(t, drain(stream))

	// and the next flagd-format change is announced again
	s.Update("src1", []model.Flag{testFlag("fs1", "a", "on")}, model.Metadata{"flagSetId": "fs1"}, false)
	require.Len(t, drain(stream), 1)
}
