//go:build unix

package runtime

import (
	"bufio"
	"context"
	"fmt"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

// TestOFREPEventStreamAnnouncesAVendorChange pins the wiring through FromConfig: a delivery a vendor format
// accepts reaches the clients of the OFREP event stream as a refetch event, though flagd's flags do not change.
func TestOFREPEventStreamAnnouncesAVendorChange(t *testing.T) {
	p := newPorts(t)
	cfg := characterizedConfig(t, p)
	vendor := filepath.Join(filepath.Dir(cfg.SyncProviders[0].URI), "vendor.json")
	require.NoError(t, os.WriteFile(vendor, []byte(`{}`), 0o600))
	cfg.SyncProviders = append(cfg.SyncProviders, coresync.SourceConfig{URI: vendor, Provider: "file", Format: "versioned"})
	rt, err := FromConfig(logger.NewLogger(nil, false), "test", cfg, withFlagd(Components{
		Endpoints:            flagdcomponents.Components().Endpoints,
		ConfigurationFormats: []configformat.Factory{versionedFactory{}},
	}))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.start(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	// the vendor file is delivered by its sync provider, once the runtime is up
	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", p.management)) //nolint:noctx // local
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 20*time.Second, 50*time.Millisecond)

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/sse", p.ofrep), nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	time.Sleep(200 * time.Millisecond)

	rt.updateAndEmit(coresync.DataSync{Source: vendor, FlagData: "two"})

	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case line := <-lines:
			if strings.Contains(line, "refetchEvaluation") {
				return
			}
		case <-deadline:
			t.Fatal("no refetch event for the vendor format's change")
		}
	}
}
