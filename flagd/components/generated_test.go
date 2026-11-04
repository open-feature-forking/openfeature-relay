package components

import (
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	otelprovider "github.com/open-feature-forking/openfeature-relay/telemetry/otel"
	"github.com/stretchr/testify/require"
)

// probed is what the generated probe prints for a factory.
type probed struct {
	Group   distribution.Group `json:"group"`
	Type    string             `json:"type"`
	Module  string             `json:"module"`
	Schemes []string           `json:"schemes"`
	Format  string             `json:"format"`
	Needs   *struct {
		Router bool   `json:"router"`
		Format string `json:"format"`
	} `json:"needs"`
}

// TestGeneratedComponentsEqualHandWritten pins that the source the Builder generates from
// distributions/flagd-full/manifest.yaml holds the same components as this package: the same factories, each in the
// module the inventory names, with the same schemes, evaluator format and needs. `make check-flagd-full-manifest`
// generates the source and sets FLAGD_FULL_GENERATED to its directory.
func TestGeneratedComponentsEqualHandWritten(t *testing.T) {
	dir := os.Getenv("FLAGD_FULL_GENERATED")
	if dir == "" {
		t.Skip("FLAGD_FULL_GENERATED names the directory of the generated source; make check-flagd-full-manifest sets it")
	}
	cmd := exec.Command("go", "run", "./probe")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	out, err := cmd.Output()
	require.NoError(t, err, "go run ./probe in %s", dir)
	var got []probed
	require.NoError(t, json.Unmarshal(out, &got))

	official, inventory := Official(), Inventory()
	moduleOf := func(group distribution.Group, typeName string) string {
		for _, info := range inventory.Components {
			if info.Group == group && info.Type == typeName {
				return info.Module
			}
		}
		return ""
	}
	var want []probed
	for _, f := range official.Sync {
		p := probed{Group: distribution.GroupSync, Type: f.Type(), Module: moduleOf(distribution.GroupSync, f.Type())}
		if s, ok := f.(coresync.SchemeFactory); ok {
			p.Schemes = s.Schemes()
		}
		want = append(want, p)
	}
	for _, f := range official.ConfigurationFormats {
		want = append(want, probed{Group: distribution.GroupConfigurationFormat, Type: f.Type(),
			Module: moduleOf(distribution.GroupConfigurationFormat, f.Type())})
	}
	for _, f := range official.Evaluators {
		want = append(want, probed{Group: distribution.GroupEvaluator, Type: f.Type(), Format: f.ConfigurationFormat(),
			Module: moduleOf(distribution.GroupEvaluator, f.Type())})
	}
	for _, f := range official.Endpoints {
		n := f.Needs()
		want = append(want, probed{Group: distribution.GroupServiceEndpoint, Type: f.Type(),
			Module: moduleOf(distribution.GroupServiceEndpoint, f.Type()),
			Needs:  &struct {
				Router bool   `json:"router"`
				Format string `json:"format"`
			}{n.Router, n.Format}})
	}

	// flagd's command selects the OpenTelemetry provider on top of Official (flagd/main.go); the generated components
	// must select it too, or the built flagd-full loses its telemetry and its /metrics with every other check green
	want = append(want, probed{Group: distribution.GroupTelemetry, Type: otelprovider.Type,
		Module: moduleOf(distribution.GroupTelemetry, otelprovider.Type)})

	byKey := func(items []probed) {
		sort.Slice(items, func(i, j int) bool {
			return string(items[i].Group)+"/"+items[i].Type < string(items[j].Group)+"/"+items[j].Type
		})
	}
	byKey(got)
	byKey(want)
	require.Equal(t, want, got)
}
