package builder

import (
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/sync/blob"
	"github.com/open-feature-forking/openfeature-relay/sync/blob/bloburi"
	buildermock "github.com/open-feature-forking/openfeature-relay/sync/builder/mock"
	"github.com/open-feature-forking/openfeature-relay/sync/file"
	flagdgrpc "github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc"
	httpsync "github.com/open-feature-forking/openfeature-relay/sync/http"
	ofoperator "github.com/open-feature-forking/openfeature-relay/sync/openfeature-operator"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// providerKind names the provider a builder made. Only this function knows the provider types.
func providerKind(s sync.ISync) string {
	switch s.(type) {
	case *file.Sync:
		return "file"
	case *httpsync.Sync:
		return "http"
	case *flagdgrpc.Sync:
		return "grpc"
	case *ofoperator.Sync:
		return "kubernetes"
	case *blob.Sync:
		return "blob"
	default:
		return "unknown"
	}
}

// blobSourceName returns the source name a blob provider delivers with. Only this function reads the
// provider's fields.
func blobSourceName(t *testing.T, s sync.ISync) string {
	t.Helper()

	b, ok := s.(*blob.Sync)
	require.True(t, ok)
	return bloburi.Join(b.Bucket, b.Object)
}

// TestSourceMappingCharacterization pins how sources are mapped to providers: the URI forms and their
// rewriting, the provider built for each provider name, the error texts, the source name a blob provider
// delivers with, the file watcher chosen by the environment and the poll schedule of a polling provider.
func TestSourceMappingCharacterization(t *testing.T) {
	t.Run("uri forms", func(t *testing.T) {
		tests := []struct {
			uri  string
			want sync.SourceConfig
		}{
			{"file:/etc/flags.json", sync.SourceConfig{URI: "/etc/flags.json", Provider: "file"}},
			{"file:relative.yaml", sync.SourceConfig{URI: "relative.yaml", Provider: "file"}},
			{"core.openfeature.dev/ns/name", sync.SourceConfig{URI: "ns/name", Provider: "kubernetes"}},
			{"http://host/flags", sync.SourceConfig{URI: "http://host/flags", Provider: "http"}},
			{"https://host/flags?x=1", sync.SourceConfig{URI: "https://host/flags?x=1", Provider: "http"}},
			{"grpc://host:8013", sync.SourceConfig{URI: "host:8013", Provider: "grpc"}},
			{"grpcs://host:8013", sync.SourceConfig{URI: "host:8013", Provider: "grpc", TLS: true}},
			{"envoy://host/svc", sync.SourceConfig{URI: "envoy://host/svc", Provider: "grpc"}},
			{"dns:///host:8013", sync.SourceConfig{URI: "dns:///host:8013", Provider: "grpc"}},
			{"uds:///tmp/sock", sync.SourceConfig{URI: "uds:///tmp/sock", Provider: "grpc"}},
			{"xds://host/svc", sync.SourceConfig{URI: "xds://host/svc", Provider: "grpc"}},
			{"gs://bucket/path/flags.json", sync.SourceConfig{URI: "gs://bucket/path/flags.json", Provider: "gcs"}},
			{"azblob://bucket/flags.yaml", sync.SourceConfig{URI: "azblob://bucket/flags.yaml", Provider: "azblob"}},
			{
				"s3://bucket/flags.json?use_path_style=true&region=eu",
				sync.SourceConfig{URI: "s3://bucket/flags.json?use_path_style=true&region=eu", Provider: "s3"},
			},
		}
		for _, tt := range tests {
			got, err := ParseSyncProviderURIs([]string{tt.uri})
			require.NoError(t, err, tt.uri)
			require.Equal(t, []sync.SourceConfig{tt.want}, got, tt.uri)
		}
	})

	t.Run("error texts", func(t *testing.T) {
		_, err := ParseSyncProviderURIs([]string{"consul://host/flags"})
		require.EqualError(t, err, "invalid sync uri argument: consul://host/flags, must start with 'file:', "+
			"'http(s)://', 'grpc(s)://', 'gs://', 'azblob://', 's3://' or 'core.openfeature.dev'")

		_, err = NewSyncBuilder().SyncsFromConfig(
			[]sync.SourceConfig{{URI: "x", Provider: "disk"}}, logger.NewLogger(nil, false))
		require.EqualError(t, err, "could not create sync provider: invalid sync provider: disk, must be one of "+
			"with 'file', 'fsnotify', 'fileinfo', 'kubernetes', 'http', 'grpc', 'gcs', 'azblob' or 's3'")
	})

	t.Run("provider kind per provider name", func(t *testing.T) {
		t.Setenv("AZURE_STORAGE_ACCOUNT", "account")
		k8s := func(sb *SyncBuilder) {
			ctrl := gomock.NewController(t)
			client := buildermock.NewMockIK8sClientBuilder(ctrl)
			client.EXPECT().GetK8sClient().Return(nil, nil)
			sb.k8sClientBuilder = client
		}
		tests := []struct {
			cfg  sync.SourceConfig
			opts []func(*SyncBuilder)
			want string
		}{
			{sync.SourceConfig{URI: "/tmp/f.json", Provider: "file"}, nil, "file"},
			{sync.SourceConfig{URI: "/tmp/f.json", Provider: "fsnotify"}, nil, "file"},
			{sync.SourceConfig{URI: "/tmp/f.json", Provider: "fileinfo"}, nil, "file"},
			{sync.SourceConfig{URI: "http://host/f", Provider: "http"}, nil, "http"},
			{sync.SourceConfig{URI: "host:8013", Provider: "grpc"}, nil, "grpc"},
			{sync.SourceConfig{URI: "ns/name", Provider: "kubernetes"}, []func(*SyncBuilder){k8s}, "kubernetes"},
			{sync.SourceConfig{URI: "gs://b/o", Provider: "gcs"}, nil, "blob"},
			{sync.SourceConfig{URI: "azblob://b/o", Provider: "azblob"}, nil, "blob"},
			{sync.SourceConfig{URI: "s3://b/o", Provider: "s3"}, nil, "blob"},
		}
		for _, tt := range tests {
			provider, _ := buildSource(t, tt.cfg, tt.opts...)
			require.Equal(t, tt.want, providerKind(provider), tt.cfg.Provider)
		}
	})

	t.Run("blob source names are the configured URIs", func(t *testing.T) {
		t.Setenv("AZURE_STORAGE_ACCOUNT", "account")
		for _, cfg := range []sync.SourceConfig{
			{URI: "gs://bucket/path/to/flags.json", Provider: "gcs"},
			{URI: "azblob://bucket/flags.yaml", Provider: "azblob"},
			{URI: "s3://bucket/flags.json", Provider: "s3"},
			{URI: "s3://bucket/path/flags.json?use_path_style=true&region=eu&endpoint=http://127.0.0.1:3900", Provider: "s3"},
		} {
			provider, _ := buildSource(t, cfg)
			require.Equal(t, cfg.URI, blobSourceName(t, provider), cfg.URI)
		}
	})

	t.Run("a file: prefix in --sources is stripped to the path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "flags.json")
		require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o600))

		for _, provider := range []string{"file", "fsnotify", "fileinfo"} {
			src := startSource(t, sync.SourceConfig{URI: "file:" + path, Provider: provider})
			// the provider reads the path, and names its deliveries by the path
			src.requireDelivery(t, sync.DataSync{FlagData: `{}`, Source: path})
		}
	})

	t.Run("file watcher by environment", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "flags.json")
		require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o600))

		watcherOf := func(t *testing.T, provider string) string {
			src := startSource(t, sync.SourceConfig{URI: path, Provider: provider})
			src.requireDelivery(t, sync.DataSync{FlagData: `{}`, Source: path})
			entries := src.logs.FilterMessage("Starting filepath sync notifier").All()
			require.Len(t, entries, 1)
			return entries[0].ContextMap()["sync"].(string)
		}

		t.Setenv("KUBERNETES_SERVICE_HOST", "")
		require.Equal(t, "fileinfo", watcherOf(t, "file"))
		require.Equal(t, "fsnotify", watcherOf(t, "fsnotify"))
		require.Equal(t, "fileinfo", watcherOf(t, "fileinfo"))

		t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
		require.Equal(t, "fsnotify", watcherOf(t, "file"))
		require.Equal(t, "fileinfo", watcherOf(t, "fileinfo"))
	})

	t.Run("poll schedule", func(t *testing.T) {
		// a poll fires when the seconds since midnight, less the offset, divide by the interval
		pollSecond := func(t *testing.T, cfg sync.SourceConfig) int {
			var requests atomic.Int32
			polled := make(chan time.Time, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
				if requests.Add(1) == 2 {
					polled <- time.Now()
				}
			}))
			t.Cleanup(server.Close)

			cfg.URI = server.URL + "/flags"
			cfg.Provider = "http"
			src := startSource(t, cfg)
			src.requireDelivery(t, sync.DataSync{FlagData: `{}`, Source: cfg.URI})

			select {
			case at := <-polled:
				h, m, s := at.Clock()
				return h*3600 + m*60 + s
			case <-time.After(10 * time.Second):
				t.Fatal("no poll")
				return 0
			}
		}

		t.Run("default interval of 5 seconds, no offset", func(t *testing.T) {
			t.Parallel()
			require.Zero(t, pollSecond(t, sync.SourceConfig{})%5)
		})
		t.Run("interval with seed", func(t *testing.T) {
			t.Parallel()
			h := fnv.New32a()
			_, _ = h.Write([]byte("pod-1"))
			offset := int(h.Sum32() % 3)
			require.Equal(t, offset, pollSecond(t, sync.SourceConfig{Interval: 3, IntervalSeed: "pod-1"})%3)
		})
	})
}
