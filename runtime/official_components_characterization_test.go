package runtime

import (
	"bytes"
	"flag"
	"fmt"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
	"os"
	"strings"
	"testing"

	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	"github.com/open-feature-forking/openfeature-relay/sync/builder/official"
	"github.com/stretchr/testify/require"
)

var updateOfficialComponents = flag.Bool("update-official-components", false,
	"rewrite testdata/official_components.golden from the current components")

const officialComponentsGolden = "testdata/official_components.golden"

// TestOfficialComponentsCharacterization pins the components flagd-full is made of: every sync component with the
// schemes and capabilities it declares, flagd's configuration format and evaluator, and the three service endpoints
// with what each needs. It is the expected output of the components probe for flagd-full (UC-011), and a flagd-full
// built by the Builder keeps all of it (UC-001 drop-in rule). Go type names are left out on purpose: a module split
// moves them.
func TestOfficialComponentsCharacterization(t *testing.T) {
	var out strings.Builder

	out.WriteString("sync components\n")
	for _, f := range official.Factories() {
		fmt.Fprintf(&out, "  %s\n", f.Type())
		if sf, ok := f.(coresync.SchemeFactory); ok {
			fmt.Fprintf(&out, "    schemes: %q\n", sf.Schemes())
		}
		fmt.Fprintf(&out, "    capabilities: %s\n", capabilities(f))
	}

	out.WriteString("configuration format\n")
	fmt.Fprintf(&out, "  %s (built in; the default of a source that names none)\n", flagdformat.Type)

	out.WriteString("evaluator\n")
	ev := flagdeval.Factory{}
	fmt.Fprintf(&out, "  %s for the configuration format %s (built in)\n", ev.Type(), ev.ConfigurationFormat())

	out.WriteString("service endpoints\n")
	for _, f := range flagdcomponents.Components().Endpoints {
		needs := f.Needs()
		fmt.Fprintf(&out, "  %s\n    needs: router=%v format=%q\n", f.Type(), needs.Router, needs.Format)
	}
	// /metrics is served by the telemetry provider's MetricsHandler; flagd start adds the OpenTelemetry provider
	// (TestStartSelectsOpenTelemetry), and the management port characterization pins what it serves.
	fmt.Fprintf(&out, "metrics handler override: %v\n", flagdcomponents.Components().MetricsHandler != nil)
	fmt.Fprintf(&out, "telemetry provider: %v\n", flagdcomponents.Components().Telemetry != nil)

	if *updateOfficialComponents {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(officialComponentsGolden, []byte(out.String()), 0o600))
		return
	}
	want, err := os.ReadFile(officialComponentsGolden)
	require.NoError(t, err, "record the current components with `go test -run TestOfficialComponentsCharacterization -update-official-components`")
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	require.Equal(t, string(want), out.String())
}

func isA[T any](v any) bool {
	_, ok := v.(T)
	return ok
}

func capabilities(f coresync.Factory) string {
	var caps []string
	if isA[coresync.FetcherFactory](f) {
		caps = append(caps, "fetcher")
	}
	if isA[coresync.StreamingFactory](f) {
		caps = append(caps, "streaming")
	}
	if isA[coresync.TriggerFactory](f) {
		caps = append(caps, "trigger")
	}
	if isA[coresync.DefaultTriggerer](f) {
		caps = append(caps, "default-triggers")
	}
	return strings.Join(caps, ",")
}
