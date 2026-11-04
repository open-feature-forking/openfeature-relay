package sync

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	syncv1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
	"github.com/stretchr/testify/require"
)

const vendorFormat = "vendor"

// vendorValue is the Value of a vendor document: it keeps the text the source delivered.
type vendorValue string

func (v vendorValue) DocumentText() string { return string(v) }

func applyVendor(t *testing.T, s *store.Store, source, flagSet, version, text string) {
	t.Helper()
	require.NoError(t, s.Apply(store.ApplyRequest{
		Format: vendorFormat, Source: source,
		Documents: []store.Document{{
			Format: vendorFormat, Source: source, FlagSet: flagSet, Version: version, Value: vendorValue(text),
		}},
	}))
}

func newVendorStore(t *testing.T, sources ...string) *store.Store {
	t.Helper()
	s, err := store.NewStore(logger.NewLogger(nil, false), sources)
	require.NoError(t, err)
	return s
}

func newFormatHandler(s store.IStore) syncHandler {
	return syncHandler{
		store:           s,
		log:             logger.NewLogger(nil, false),
		metricsRecorder: flagdService.NoopMetrics(),
		formats:         []string{"flagd", vendorFormat},
		changes:         newChangeSignal(),
	}
}

func formatRequest(format, selector string) *connect.Request[syncv1.FetchAllFlagsRequest] {
	req := connect.NewRequest(&syncv1.FetchAllFlagsRequest{})
	if format != "" {
		req.Header().Set(FormatHeader, format)
	}
	if selector != "" {
		req.Header().Set(flagdService.FLAGD_SELECTOR_HEADER, selector)
	}
	return req
}

// A request without the header, or naming flagd's own format, takes today's path: what it gets is what the flagd path
// produced before the header existed, with or without other formats in the distribution.
func TestNoFormatHeaderServesFlagdUnchanged(t *testing.T) {
	flagStore, _ := getSimpleFlagStore(t)
	want, err := fetchAllFlags(context.Background(), flagStore, "")
	require.NoError(t, err)

	for _, handler := range []syncHandler{newFormatHandler(flagStore), {store: flagStore, log: logger.NewLogger(nil, false)}} {
		for _, format := range []string{"", "flagd"} {
			resp, err := handler.FetchAllFlags(context.Background(), formatRequest(format, ""))
			require.NoError(t, err, format)
			require.Equal(t, string(want), resp.Msg.GetFlagConfiguration(), format)
		}
	}
}

func TestFormatHeaderServesVendorDocument(t *testing.T) {
	s := newVendorStore(t, "s1", "s2", "s3")
	applyVendor(t, s, "s1", "a", "v1", `{"env":"a"}`)
	applyVendor(t, s, "s2", "b", "v1", "environment b, not JSON at all")
	applyVendor(t, s, "s3", "", "v1", "the unnamed flag set")
	handler := newFormatHandler(s)

	tests := []struct{ name, selector, want string }{
		{"by source", "source=s1", `{"env":"a"}`},
		{"by flag set", "flagSetId=b", "environment b, not JSON at all"},
		{"by the unnamed flag set", "flagSetId=", "the unnamed flag set"},
	}
	for _, tt := range tests {
		t.Run("FetchAllFlags "+tt.name, func(t *testing.T) {
			resp, err := handler.FetchAllFlags(context.Background(), formatRequest(vendorFormat, tt.selector))
			require.NoError(t, err)
			require.Equal(t, tt.want, resp.Msg.GetFlagConfiguration(), "the text, unchanged")
		})
		t.Run("SyncFlags "+tt.name, func(t *testing.T) {
			stream := newMockSyncFlagsStream()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			header := http.Header{FormatHeader: {vendorFormat}, flagdService.FLAGD_SELECTOR_HEADER: {tt.selector}}
			go func() { done <- handler.syncFlags(ctx, header, "", "", stream) }()

			select {
			case <-stream.respReady:
				require.Equal(t, tt.want, stream.GetLastResponse().GetFlagConfiguration())
			case <-time.After(5 * time.Second):
				t.Fatal("no message")
			}
			cancel()
			require.NoError(t, <-done)
		})
	}

	t.Run("the only document needs no selector", func(t *testing.T) {
		only := newVendorStore(t, "s1")
		applyVendor(t, only, "s1", "", "v1", "the one")
		resp, err := newFormatHandler(only).FetchAllFlags(context.Background(), formatRequest(vendorFormat, ""))
		require.NoError(t, err)
		require.Equal(t, "the one", resp.Msg.GetFlagConfiguration())
	})

	t.Run("the flags route serves the text with its ETag", func(t *testing.T) {
		h := httpHandler{store: s, log: logger.NewLogger(nil, false), modTime: &modTime{}, formats: handler.formats}
		req := httptest.NewRequest(http.MethodGet, flagsPath, nil)
		req.Header.Set(FormatHeader, vendorFormat)
		req.Header.Set(flagdService.FLAGD_SELECTOR_HEADER, "source=s1")

		rec := serve(h, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, `{"env":"a"}`, rec.Body.String())
		require.Equal(t, etagOf([]byte(`{"env":"a"}`)), rec.Header().Get("ETag"))
		require.Contains(t, rec.Header().Get("Vary"), FormatHeader)
	})
}

func TestSeveralDocumentsInvalidArgument(t *testing.T) {
	s := newVendorStore(t, "s1", "s2")
	applyVendor(t, s, "s1", "a", "v1", "one")
	applyVendor(t, s, "s2", "b", "v1", "two")
	handler := newFormatHandler(s)

	_, err := handler.FetchAllFlags(context.Background(), formatRequest(vendorFormat, ""))

	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	require.ErrorContains(t, err, `2 vendor documents match`)
	require.ErrorContains(t, err, `source "s1" flag set "a"`)
	require.ErrorContains(t, err, `source "s2" flag set "b"`)

	stream := newMockSyncFlagsStream()
	err = handler.syncFlags(context.Background(), http.Header{FormatHeader: {vendorFormat}}, "", "", stream)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	h := httpHandler{store: s, log: logger.NewLogger(nil, false), modTime: &modTime{}, formats: handler.formats}
	req := httptest.NewRequest(http.MethodGet, flagsPath, nil)
	req.Header.Set(FormatHeader, vendorFormat)
	require.Equal(t, http.StatusBadRequest, serve(h, req).Code)
}

func TestUnknownFormatInvalidArgument(t *testing.T) {
	flagStore, _ := getSimpleFlagStore(t)
	handler := newFormatHandler(flagStore)

	_, err := handler.FetchAllFlags(context.Background(), formatRequest("nope", ""))

	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	require.ErrorContains(t, err, "flagd, "+vendorFormat, "the formats the distribution has")
	require.NotContains(t, err.Error(), "nope", "the request's value is not echoed")

	err = handler.syncFlags(context.Background(), http.Header{FormatHeader: {"nope"}}, "", "", newMockSyncFlagsStream())
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	h := httpHandler{store: flagStore, log: logger.NewLogger(nil, false), modTime: &modTime{}, formats: handler.formats}
	req := httptest.NewRequest(http.MethodGet, flagsPath, nil)
	req.Header.Set(FormatHeader, "nope")
	rec := serve(h, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.NotContains(t, rec.Body.String(), "nope")
}

func TestDocumentWithoutTextInternal(t *testing.T) {
	s := newVendorStore(t, "s1")
	require.NoError(t, s.Apply(store.ApplyRequest{
		Format: vendorFormat, Source: "s1",
		Documents: []store.Document{{Format: vendorFormat, Source: "s1", Version: "v1", Value: map[string]string{"a": "b"}}},
	}))

	_, err := newFormatHandler(s).FetchAllFlags(context.Background(), formatRequest(vendorFormat, ""))

	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	require.ErrorContains(t, err, vendorFormat)
	require.ErrorContains(t, err, "store.TextDocument", "it points an extension author at the seam")
}

func TestNoDocumentNotFoundThenStreamWaits(t *testing.T) {
	s := newVendorStore(t, "s1")
	handler := newFormatHandler(s)

	_, err := handler.FetchAllFlags(context.Background(), formatRequest(vendorFormat, ""))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	stream := newMockSyncFlagsStream()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- handler.syncFlags(ctx, http.Header{FormatHeader: {vendorFormat}}, "", "", stream)
	}()
	select {
	case <-stream.respReady:
		t.Fatal("a message before the document exists")
	case <-time.After(200 * time.Millisecond):
	}

	applyVendor(t, s, "s1", "", "v1", "arrived")
	handler.changes.broadcast()

	select {
	case <-stream.respReady:
		require.Equal(t, "arrived", stream.GetLastResponse().GetFlagConfiguration())
	case <-time.After(5 * time.Second):
		t.Fatal("no message after the document appeared")
	}
	cancel()
	require.NoError(t, <-done)
}

// The stream sends the document again when its version changes, and sends nothing for a change that leaves it alone.
func TestUnchangedVersionSendsNothing(t *testing.T) {
	s := newVendorStore(t, "s1")
	applyVendor(t, s, "s1", "", "v1", "first")
	handler := newFormatHandler(s)
	stream := newMockSyncFlagsStream()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- handler.syncFlags(ctx, http.Header{FormatHeader: {vendorFormat}}, "", "", stream)
	}()
	expect := func(want string) {
		t.Helper()
		select {
		case <-stream.respReady:
			require.Equal(t, want, stream.GetLastResponse().GetFlagConfiguration())
		case <-time.After(5 * time.Second):
			t.Fatalf("no message, wanted %q", want)
		}
	}
	expect("first")

	applyVendor(t, s, "s1", "", "v1", "first")
	handler.changes.broadcast()
	select {
	case <-stream.respReady:
		t.Fatal("a message for an unchanged version")
	case <-time.After(300 * time.Millisecond):
	}

	applyVendor(t, s, "s1", "", "v2", "second")
	handler.changes.broadcast()
	expect("second")

	cancel()
	require.NoError(t, <-done)
}

// The endpoint wakes a stream of a vendor format through the configuration change events of the runtime, as it dates
// its flags: a stream receives the new text of a document whose version changed, over the wire.
func TestVendorStreamFollowsConfigurationChangeEvents(t *testing.T) {
	const port = 18090

	s := newVendorStore(t, "s1")
	applyVendor(t, s, "s1", "", "v1", "first")
	bus := events.NewBus()
	initialized := make(chan struct{})
	close(initialized)

	endpoint, err := Factory{}.Create(service.Settings{
		Store: s, Bus: bus, Initialized: initialized, Logger: logger.NewLogger(nil, false),
		Formats: []string{"flagd", vendorFormat},
		Config:  map[string]any{ConfigKey: SvcConfigurations{Port: port, Sources: []string{"s1"}}},
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- endpoint.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})

	client := connectClient(t, fmt.Sprintf("http://localhost:%d", port), "")
	fetch := connect.NewRequest(&syncv1.FetchAllFlagsRequest{})
	fetch.Header().Set(FormatHeader, vendorFormat)
	fetchWithRetry(t, client, fetch)

	req := connect.NewRequest(&syncv1.SyncFlagsRequest{})
	req.Header().Set(FormatHeader, vendorFormat)
	stream, err := client.SyncFlags(ctx, req)
	require.NoError(t, err)
	defer stream.Close()
	require.True(t, stream.Receive(), "the first document: %v", stream.Err())
	require.Equal(t, "first", stream.Msg().GetFlagConfiguration())

	applyVendor(t, s, "s1", "", "v2", "second")
	events.PublishConfigurationChange(bus, events.ConfigurationChange{Source: "s1", Format: vendorFormat, Version: "v2"})

	require.True(t, stream.Receive(), "the changed document: %v", stream.Err())
	require.Equal(t, "second", stream.Msg().GetFlagConfiguration())
}
