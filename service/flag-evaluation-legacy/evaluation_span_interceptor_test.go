package flagevallegacy

import (
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	evaluationV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	schemaConnectV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/schema/v1/schemav1connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	iservice "github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	evaluation "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	"github.com/stretchr/testify/require"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// TestEvaluationSpansWithInterceptor pins the span tree of the flag evaluation RPCs with the ConnectRPC
// interceptor FromConfig installs: the interceptor's server span above the service's span and the evaluator's.
// TestEvaluationSpans builds the endpoint without handler options.
func TestEvaluationSpansWithInterceptor(t *testing.T) {
	log := logger.NewLogger(nil, false)
	flagStore, err := store.NewStore(log, []string{spanTestSource})
	require.NoError(t, err)
	eval := evaluation.NewEvaluator(log, flagStore)
	require.NoError(t, eval.SetState(coresync.DataSync{FlagData: spanTestFlags, Source: spanTestSource}))
	url := serveComposed(t, eval, flagStore, spanRecorder, iservice.Configuration{ServiceName: "spanTest"})
	clients := evaluationSpanClients{
		legacy: schemaConnectV1.NewServiceClient(http.DefaultClient, url),
		v1:     evaluationV1.NewServiceClient(http.DefaultClient, url),
	}

	tests := []struct {
		call, key, selector string
		wantErr             bool
	}{
		{call: "legacy/Boolean", key: "bool-flag"},
		{call: "legacy/All"},
		{call: "v1/Boolean", key: "bool-flag"},
		{call: "v1/Boolean", key: "missing", wantErr: true},
		{call: "v1/Object", key: "object-flag"},
		{call: "v1/All"},
		{call: "v1/All", selector: invalidSelector, wantErr: true},
	}

	var got strings.Builder
	for _, tt := range tests {
		spanRecorder.Reset()
		err := evaluationCalls[tt.call](t.Context(), clients, tt.key, tt.selector)
		if tt.wantErr {
			require.Error(t, err, subtestName(tt.call, tt.key, tt.selector))
		} else {
			require.NoError(t, err, subtestName(tt.call, tt.key, tt.selector))
		}
		got.WriteString("# " + subtestName(tt.call, tt.key, tt.selector) + "\n")
		got.WriteString(renderSpanTree(spanRecorder.Ended()))
	}

	path := filepath.Join("testdata", "evaluation_spans_interceptor.golden")
	if *updateGolden {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got.String()), 0o600))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run go test -update to create %s", path)
	require.Equal(t, string(want), got.String())
}
