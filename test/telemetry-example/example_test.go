package telemetryexample_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry/telemetrytest"
	"github.com/stretchr/testify/require"

	telemetryexample "github.com/open-feature/flagd/test/telemetry-example"
)

// file is an in-memory file whose writes block while it stalls.
type file struct {
	mu    sync.Mutex
	gate  chan struct{}
	buf   bytes.Buffer
	lines []telemetryexample.Line
}

func newFile() *file {
	gate := make(chan struct{})
	close(gate)
	return &file{gate: gate}
}

func (f *file) stall(testing.TB) func() {
	f.mu.Lock()
	gate := make(chan struct{})
	f.gate = gate
	f.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

func (f *file) Write(p []byte) (int, error) {
	f.mu.Lock()
	gate := f.gate
	f.mu.Unlock()
	<-gate
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.Write(p)
}

func (f *file) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	sc := bufio.NewScanner(bytes.NewReader(f.buf.Bytes()))
	for sc.Scan() {
		var l telemetryexample.Line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return err
		}
		f.lines = append(f.lines, l)
	}
	return nil
}

func (f *file) observed(testing.TB) telemetrytest.Observation {
	f.mu.Lock()
	defer f.mu.Unlock()
	var o telemetrytest.Observation
	for _, l := range f.lines {
		if l.Type == "span" {
			o.Spans = append(o.Spans, l.Name)
		} else {
			o.Metrics = append(o.Metrics, l.Name)
		}
	}
	return o
}

func TestContract(t *testing.T) {
	var (
		mu     sync.Mutex
		latest *file
	)
	current := func() *file {
		mu.Lock()
		defer mu.Unlock()
		return latest
	}
	f := telemetryexample.Factory{Open: func(string) (io.WriteCloser, error) {
		mu.Lock()
		defer mu.Unlock()
		latest = newFile()
		return latest, nil
	}}
	telemetrytest.Run(t, f, telemetrytest.Inputs{
		Config:        map[string]any{telemetryexample.SettingPath: "telemetry.jsonl"},
		InvalidConfig: map[string]any{"pathh": "telemetry.jsonl"},
		FaultyValue:   "pathh",
		StallBackend:  func(t testing.TB) func() { return current().stall(t) },
		Observed:      func(t testing.TB) telemetrytest.Observation { return current().observed(t) },
	})
}

func create(t *testing.T, config map[string]any) (telemetry.Provider, error) {
	t.Helper()
	return telemetryexample.Factory{}.Create(context.Background(), telemetry.Settings{
		Logger:           logger.NewLogger(nil, false),
		DistributionName: "flagd",
		Config:           config,
	})
}

// TestWritesJSONLines records what an endpoint records, shuts down and reads the file back.
func TestWritesJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	p, err := create(t, map[string]any{telemetryexample.SettingPath: path})
	require.NoError(t, err)

	ctx, span := p.Tracer("flagd.ofrep.v1").Start(context.Background(), "resolveAll",
		telemetry.WithSpanKind(telemetry.SpanKindServer))
	span.SetAttributes(telemetry.Int("feature_flag.count", 2))
	span.RecordError(errors.New("boom"))
	span.End()
	impressions := p.Meter("flagd").Int64Counter("feature_flag.flagd.impression")
	impressions.Add(ctx, 1, telemetry.String("feature_flag.key", "a"))
	impressions.Add(ctx, 2, telemetry.String("feature_flag.key", "a"))
	impressions.Add(ctx, 1, telemetry.String("feature_flag.key", "b"))

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotModified) })
	h := p.HTTPServerMetrics(p.HTTPServerSpans(handler, "flagd.ofrep"),
		telemetry.HTTPMetricsOptions{Service: "flagd", Route: "/ofrep/v1/evaluate/flags"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", nil))
	require.Equal(t, http.StatusNotModified, rec.Code)

	require.NoError(t, p.Shutdown(context.Background()))
	require.NoError(t, p.Shutdown(context.Background()), "a second shutdown")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var spans, metrics []telemetryexample.Line
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		var l telemetryexample.Line
		require.NoError(t, json.Unmarshal(sc.Bytes(), &l))
		if l.Type == "span" {
			spans = append(spans, l)
		} else {
			metrics = append(metrics, l)
		}
	}

	require.Len(t, spans, 2)
	require.Equal(t, "resolveAll", spans[0].Name)
	require.Equal(t, "server", spans[0].Kind)
	require.Equal(t, "boom", spans[0].Error)
	require.EqualValues(t, 2, spans[0].Attributes["feature_flag.count"])
	require.Equal(t, "flagd.ofrep", spans[1].Name)
	require.EqualValues(t, 304, spans[1].Attributes["http.response.status_code"])

	byKey := map[string]telemetryexample.Line{}
	for _, m := range metrics {
		key, _ := m.Attributes["feature_flag.key"].(string)
		byKey[m.Name+"/"+key] = m
	}
	require.Len(t, metrics, 3)
	require.EqualValues(t, 3, byKey["feature_flag.flagd.impression/a"].Value)
	require.EqualValues(t, 2, byKey["feature_flag.flagd.impression/a"].Count)
	require.EqualValues(t, 1, byKey["feature_flag.flagd.impression/b"].Value)
	duration := byKey["http.server.request.duration/"]
	require.Equal(t, "histogram", duration.Kind)
	require.Equal(t, "/ofrep/v1/evaluate/flags", duration.Attributes["http.route"])
	require.Equal(t, "304", duration.Attributes["http.response.status_code"])
}

func TestInvalidSettingsAreNamed(t *testing.T) {
	tests := map[string]struct {
		config map[string]any
		want   string
	}{
		"unknown":        {map[string]any{"file": "x"}, `"file"`},
		"missing":        {nil, `"path" is missing`},
		"not a string":   {map[string]any{telemetryexample.SettingPath: 42}, "42"},
		"unwritable":     {map[string]any{telemetryexample.SettingPath: filepath.Join(t.TempDir(), "missing", "t.jsonl")}, "missing"},
		"an empty value": {map[string]any{telemetryexample.SettingPath: ""}, `"path"`},
	}
	for name, tt := range tests {
		_, err := create(t, tt.config)
		require.ErrorContains(t, err, tt.want, name)
		require.ErrorContains(t, err, `"jsonlines"`, name)
	}
}
