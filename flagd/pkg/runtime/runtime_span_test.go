package runtime

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/open-feature/flagd/core/pkg/logger"
	"github.com/open-feature/flagd/core/pkg/store"
	coresync "github.com/open-feature/flagd/core/pkg/sync"
	"github.com/open-feature/flagd/flagd/pkg/evaluation"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const spanTestSource = "test-source"

// Tracers may be fetched when components are built, so the provider is installed before any test
// builds one. A global provider cannot be restored; tests reset the recorder instead.
var spanRecorder = tracetest.NewSpanRecorder()

func TestMain(m *testing.M) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder)))
	os.Exit(m.Run())
}

// renderSpans renders ended spans in start order, one per line, without timestamps or IDs. A span
// with a parent is indented beneath it.
func renderSpans(spans []sdktrace.ReadOnlySpan) string {
	sorted := append([]sdktrace.ReadOnlySpan(nil), spans...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartTime().Before(sorted[j].StartTime()) })

	present := map[string]bool{}
	for _, s := range sorted {
		present[s.SpanContext().SpanID().String()] = true
	}

	var out strings.Builder
	for _, s := range sorted {
		if present[s.Parent().SpanID().String()] {
			out.WriteString("  ")
		}
		out.WriteString(renderSpan(s))
		out.WriteString("\n")
	}
	return out.String()
}

func renderSpan(s sdktrace.ReadOnlySpan) string {
	attrs := make([]string, 0, len(s.Attributes()))
	for _, a := range s.Attributes() {
		attrs = append(attrs, fmt.Sprintf("%s=%s", a.Key, a.Value.Emit()))
	}
	sort.Strings(attrs)

	line := fmt.Sprintf("%s/%s[%s]{%s}", s.InstrumentationScope().Name, s.Name(), s.SpanKind(), strings.Join(attrs, ","))
	if st := s.Status(); st.Code == codes.Error {
		line += fmt.Sprintf(" status=Error(%q)", st.Description)
	}
	for _, e := range s.Events() {
		line += " event=" + e.Name
	}
	return line
}

// TestFlagSyncSpan pins the flagSync span that updating the evaluator from a sync payload produces.
func TestFlagSyncSpan(t *testing.T) {
	tests := []struct {
		name        string
		flagData    string
		wantEmitted []string
		want        string
	}{
		{
			name:        "valid flag configuration",
			flagData:    `{"flags": {"bool-flag": {"state": "ENABLED", "variants": {"on": true, "off": false}, "defaultVariant": "on"}}}`,
			wantEmitted: []string{spanTestSource},
			want:        "jsonEvaluator/flagSync[internal]{feature_flag.source=test-source}\n",
		},
		{
			name:        "rejected flag configuration",
			flagData:    `{"flags": {"bool-flag": {"state": "ENABLED", "variants": {}, "defaultVariant": "on"}}`,
			wantEmitted: nil,
			want:        "jsonEvaluator/flagSync[internal]{feature_flag.source=test-source} status=Error(\"flagSync error\") event=exception\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := logger.NewLogger(nil, false)
			flagStore, err := store.NewStore(log, []string{spanTestSource})
			require.NoError(t, err)
			syncService := &recordingSyncService{}
			runtime := Runtime{
				Evaluator:       evaluation.NewEvaluator(log, flagStore),
				Logger:          log,
				SyncService:     syncService,
				sourceReadiness: map[string]bool{spanTestSource: false},
			}
			spanRecorder.Reset()

			runtime.updateAndEmit(coresync.DataSync{FlagData: tt.flagData, Source: spanTestSource})

			require.Equal(t, tt.wantEmitted, syncService.emitted)
			require.Equal(t, tt.want, renderSpans(spanRecorder.Ended()))
		})
	}
}
