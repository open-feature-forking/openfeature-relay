package sse

import (
	"testing"
	"time"

	"github.com/launchdarkly/eventsource"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testFormats are the configuration formats of the tests' distribution: flagd's own and a vendor's.
var testFormats = []string{"flagd", "vendor"}

func mustSelector(t *testing.T, expr string) store.Selector {
	t.Helper()
	s, err := store.NewSelector(expr)
	require.NoError(t, err)
	return s
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.NewStore(logger.NewLogger(nil, false), []string{"src1", "src2"})
	require.NoError(t, err)
	return s
}

// deliver writes the document of one flag set as a configuration format does once it accepted a delivery, and
// publishes the configuration change event the runtime publishes for it.
func deliver(t *testing.T, st *store.Store, bus *events.Bus, format, source, flagSet, version string, keys ...string) {
	t.Helper()
	require.NoError(t, st.Apply(store.ApplyRequest{
		Format: format, Source: source, Incremental: true,
		Documents: []store.Document{{Format: format, Source: source, FlagSet: flagSet, Version: version, FlagKeys: keys}},
	}))
	events.PublishConfigurationChange(bus, events.ConfigurationChange{
		Time: time.Now(), Source: source, Format: format, FlagSet: flagSet, Version: version, Updated: keys,
	})
}

// newTracker builds a Tracker over a real eventsource server, so publishes are exercised.
func newTracker(t *testing.T, s *store.Store, bus *events.Bus) *Tracker {
	t.Helper()
	es := eventsource.NewServer()
	t.Cleanup(es.Close)

	tr := NewTracker(logger.NewLogger(nil, false), s, es, bus, testFormats)
	t.Cleanup(tr.Close)
	return tr
}

// subscribe registers a channel and ties its release to the test cleanup.
func subscribe(t *testing.T, tr *Tracker, expr string) {
	t.Helper()
	release, err := tr.Subscribe(expr, mustSelector(t, expr))
	require.NoError(t, err)
	t.Cleanup(release)
}

func etagOf(t *testing.T, tr *Tracker, channel string) (string, int64) {
	t.Helper()
	etag, lastModified, ok := tr.Version(channel)
	require.True(t, ok, "no version for channel %q", channel)
	return etag, lastModified
}

func TestEtag_FromDocumentVersions(t *testing.T) {
	st := newTestStore(t)
	bus := events.NewBus()
	tr := newTracker(t, st, bus)

	sel := mustSelector(t, "flagSetId=fs1")
	none := tr.etag(&sel)

	deliver(t, st, bus, "flagd", "src1", "fs1", "v1", "a", "b")
	one := tr.etag(&sel)
	assert.NotEqual(t, none, one, "a document behind the selector")
	assert.Equal(t, one, tr.etag(&sel), "stable")

	// the keys of a document are not part of the version; its version is
	deliver(t, st, bus, "flagd", "src1", "fs1", "v1", "a", "b", "c")
	assert.Equal(t, one, tr.etag(&sel), "the same version, whatever the keys")

	deliver(t, st, bus, "flagd", "src1", "fs1", "v2", "a", "b")
	two := tr.etag(&sel)
	assert.NotEqual(t, one, two, "a new version")

	// a document of another flag set or another source is not behind the selector
	deliver(t, st, bus, "flagd", "src1", "fs2", "v1", "z")
	assert.Equal(t, two, tr.etag(&sel))
	deliver(t, st, bus, "vendor", "src2", "fs1", "v1", "z")
	assert.NotEqual(t, two, tr.etag(&sel), "a vendor-format document of the flag set is")

	// the selector scopes the hash
	other := mustSelector(t, "source=src1")
	assert.NotEqual(t, tr.etag(&sel), tr.etag(&other))
}

func TestTracker_SharesOneSubscriptionPerChannel(t *testing.T) {
	tr := newTracker(t, newTestStore(t), events.NewBus())

	subscribe(t, tr, "flagSetId=fs1")
	subscribe(t, tr, "flagSetId=fs1")
	assert.Len(t, tr.Channels(), 1, "subscribers on one channel share a subscription")

	subscribe(t, tr, "flagSetId=fs2")
	assert.Len(t, tr.Channels(), 2, "a distinct channel needs its own")
}

func TestTracker_Version_NoSubscription(t *testing.T) {
	tr := newTracker(t, newTestStore(t), events.NewBus())

	_, _, ok := tr.Version("flagSetId=fs1")
	assert.False(t, ok, "no live stream for the channel means no ETag and no 304")
}

func TestTracker_ReleaseTearsDownLastSubscriberOnly(t *testing.T) {
	tr := newTracker(t, newTestStore(t), events.NewBus())

	releaseA, err := tr.Subscribe("flagSetId=fs1", mustSelector(t, "flagSetId=fs1"))
	require.NoError(t, err)
	releaseB, err := tr.Subscribe("flagSetId=fs1", mustSelector(t, "flagSetId=fs1"))
	require.NoError(t, err)

	releaseA()
	assert.Len(t, tr.Channels(), 1, "one subscriber leaving must not tear down a shared channel")

	releaseB()
	assert.Empty(t, tr.Channels())
	_, _, ok := tr.Version("flagSetId=fs1")
	assert.False(t, ok)
}

func TestTracker_IdenticalDeliveryIsSuppressed(t *testing.T) {
	st := newTestStore(t)
	bus := events.NewBus()
	deliver(t, st, bus, "flagd", "src1", "fs1", "v1", "a")

	tr := newTracker(t, st, bus)
	subscribe(t, tr, "flagSetId=fs1")
	_, firstModified := etagOf(t, tr, "flagSetId=fs1")

	// the same version again, announced: nothing changed behind the selector
	deliver(t, st, bus, "flagd", "src1", "fs1", "v1", "a")
	time.Sleep(200 * time.Millisecond)

	_, stillModified := etagOf(t, tr, "flagSetId=fs1")
	assert.Equal(t, firstModified, stillModified, "lastModified must not move when nothing changed")
}

func TestTracker_VersionTracksChanges(t *testing.T) {
	st := newTestStore(t)
	bus := events.NewBus()
	deliver(t, st, bus, "flagd", "src1", "fs1", "v1", "a")

	tr := newTracker(t, st, bus)
	subscribe(t, tr, "flagSetId=fs1")
	before, _ := etagOf(t, tr, "flagSetId=fs1")

	deliver(t, st, bus, "flagd", "src1", "fs1", "v2", "a")
	require.Eventually(t, func() bool {
		etag, _, _ := tr.Version("flagSetId=fs1")
		return etag != before
	}, 3*time.Second, 5*time.Millisecond, "the ETag must move when the document version does")
}

// TestTracker_MissedRecomputesEveryChannel pins that an event that reports missed events concerns every channel,
// whatever it says, because the tracker cannot tell what it missed.
func TestTracker_MissedRecomputesEveryChannel(t *testing.T) {
	st := newTestStore(t)
	bus := events.NewBus()
	deliver(t, st, bus, "flagd", "src1", "fs1", "v1", "a")

	tr := newTracker(t, st, bus)
	subscribe(t, tr, "flagSetId=fs1")
	before, _ := etagOf(t, tr, "flagSetId=fs1")

	// the document changes unannounced; the next event is for another flag set but reports missed events
	require.NoError(t, st.Apply(store.ApplyRequest{
		Format: "flagd", Source: "src1", Incremental: true,
		Documents: []store.Document{{Format: "flagd", Source: "src1", FlagSet: "fs1", Version: "v2", FlagKeys: []string{"a"}}},
	}))
	events.PublishConfigurationChange(bus, events.ConfigurationChange{Source: "src2", Format: "flagd", FlagSet: "fs9", Version: "v1", Missed: 2})

	require.Eventually(t, func() bool {
		etag, _, _ := tr.Version("flagSetId=fs1")
		return etag != before
	}, 3*time.Second, 5*time.Millisecond)
}

func TestTracker_CloseRejectsNewSubscribers(t *testing.T) {
	tr := newTracker(t, newTestStore(t), events.NewBus())
	tr.Close()

	_, err := tr.Subscribe("flagSetId=fs1", mustSelector(t, "flagSetId=fs1"))
	assert.Error(t, err)
}

func TestTracker_ReleaseAfterCloseIsANoOp(t *testing.T) {
	es := eventsource.NewServer()
	defer es.Close()
	tr := NewTracker(logger.NewLogger(nil, false), newTestStore(t), es, events.NewBus(), testFormats)

	release, err := tr.Subscribe("flagSetId=fs1", mustSelector(t, "flagSetId=fs1"))
	require.NoError(t, err)

	require.NotPanics(t, tr.Close)
	assert.Empty(t, tr.Channels())
	assert.NotPanics(t, release)
}
