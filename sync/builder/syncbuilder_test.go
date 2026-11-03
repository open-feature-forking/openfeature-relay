package builder

import (
	"errors"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	buildermock "github.com/open-feature-forking/openfeature-relay/sync/builder/mock"
	flagdgrpc "github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc"
	ofoperator "github.com/open-feature-forking/openfeature-relay/sync/openfeature-operator"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestSyncBuilder_SyncFromURI(t *testing.T) {
	type args struct {
		uri    string
		logger *logger.Logger
	}
	tests := []struct {
		name       string
		args       args
		injectFunc func(builder *SyncBuilder)
		want       sync.ISync
		wantErr    bool
	}{
		{
			name: "kubernetes sync",
			args: args{
				uri:    "core.openfeature.dev/ff-config",
				logger: logger.NewLogger(nil, false),
			},
			injectFunc: func(builder *SyncBuilder) {
				ctrl := gomock.NewController(t)

				mockClientBuilder := buildermock.NewMockIK8sClientBuilder(ctrl)
				mockClientBuilder.EXPECT().GetK8sClient().Times(1).Return(nil, nil)

				builder.k8sClientBuilder = mockClientBuilder
			},
			want:    &ofoperator.Sync{},
			wantErr: false,
		},
		{
			name: "kubernetes sync - error when retrieving config",
			args: args{
				uri:    "core.openfeature.dev/ff-config",
				logger: logger.NewLogger(nil, false),
			},
			injectFunc: func(builder *SyncBuilder) {
				ctrl := gomock.NewController(t)

				mockClientBuilder := buildermock.NewMockIK8sClientBuilder(ctrl)
				mockClientBuilder.EXPECT().GetK8sClient().Times(1).Return(nil, errors.New("oops"))

				builder.k8sClientBuilder = mockClientBuilder
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "file sync",
			args: args{
				uri:    "file:my-file",
				logger: logger.NewLogger(nil, false),
			},
			want:    &fetching.Provider{},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := NewSyncBuilder()

			if tt.injectFunc != nil {
				tt.injectFunc(sb)
			}

			got, err := sb.SyncFromURI(tt.args.uri, tt.args.logger)
			if tt.wantErr {
				require.NotNil(t, err)
				require.Nil(t, got)
			} else {
				require.Nil(t, err)
				require.IsType(t, tt.want, got)
			}
		})
	}
}


func Test_SyncsFromFromConfig(t *testing.T) {
	lg := logger.NewLogger(nil, false)

	type args struct {
		logger  *logger.Logger
		sources []sync.SourceConfig
	}

	tests := []struct {
		name       string
		args       args
		injectFunc func(builder *SyncBuilder)
		wantSyncs  []sync.ISync
		wantErr    bool
	}{
		{
			name: "Empty",
			args: args{
				logger:  lg,
				sources: []sync.SourceConfig{},
			},
			wantSyncs: nil,
			wantErr:   false,
		},
		{
			name: "Error",
			args: args{
				logger: lg,
				sources: []sync.SourceConfig{
					{
						URI:      "fake",
						Provider: "disk",
					},
				},
			},
			wantSyncs: nil,
			wantErr:   true,
		},
		{
			name: "single",
			args: args{
				logger: lg,
				sources: []sync.SourceConfig{
					{
						URI:        "grpc://host:port",
						Provider:   syncProviderGrpc,
						ProviderID: "myapp",
						CertPath:   "/tmp/ca.cert",
						Selector:   "source=database",
					},
				},
			},
			wantSyncs: []sync.ISync{
				&flagdgrpc.Sync{},
			},
			wantErr: false,
		},
		{
			name: "grpc-with-msg-size",
			args: args{
				logger: lg,
				sources: []sync.SourceConfig{
					{
						URI:        "grpc://host:port",
						Provider:   syncProviderGrpc,
						ProviderID: "myapp",
						CertPath:   "/tmp/ca.cert",
						Selector:   "source=database",
						MaxMsgSize: 10,
					},
				},
			},
			wantSyncs: []sync.ISync{
				&flagdgrpc.Sync{},
			},
			wantErr: false,
		},
		{
			name: "combined",
			injectFunc: func(builder *SyncBuilder) {
				t.Setenv("AZURE_STORAGE_ACCOUNT", "myaccount")

				ctrl := gomock.NewController(t)

				mockClientBuilder := buildermock.NewMockIK8sClientBuilder(ctrl)
				mockClientBuilder.EXPECT().GetK8sClient().Times(1).Return(nil, nil)

				builder.k8sClientBuilder = mockClientBuilder
			},
			args: args{
				logger: lg,
				sources: []sync.SourceConfig{
					{
						URI:        "grpc://host:port",
						Provider:   syncProviderGrpc,
						ProviderID: "myapp",
						CertPath:   "/tmp/ca.cert",
						Selector:   "source=database",
					},
					{
						URI:        "https://host:port",
						Provider:   syncProviderHTTP,
						AuthHeader: "scheme credentials/token",
					},
					{
						URI:      "/tmp/flags.json",
						Provider: syncProviderFile,
					},
					{
						URI:      "my-namespace/my-flags",
						Provider: syncProviderKubernetes,
					},
					{
						URI:      "gs://bucket/path/to/file",
						Provider: syncProviderGcs,
					},
					{
						URI:      "azblob://bucket/path/to/file",
						Provider: syncProviderAzblob,
					},
					{
						URI:      "s3://bucket/path/to/file",
						Provider: syncProviderS3,
					},
				},
			},
			wantSyncs: []sync.ISync{
				&flagdgrpc.Sync{},
				&fetching.Provider{},
				&fetching.Provider{},
				&ofoperator.Sync{},
				&fetching.Provider{},
				&fetching.Provider{},
				&fetching.Provider{},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := NewSyncBuilder()

			if tt.injectFunc != nil {
				tt.injectFunc(sb)
			}
			syncs, err := sb.SyncsFromConfig(tt.args.sources, tt.args.logger)
			if (err != nil) != tt.wantErr {
				t.Errorf("syncProvidersFromConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			require.Len(t, syncs, len(tt.wantSyncs))

			// check if we got the expected sync types
			for index, wantType := range tt.wantSyncs {
				require.IsType(t, wantType, syncs[index])
			}
		})
	}
}

func Test_GrpcIncrementalUpdates(t *testing.T) {
	lg := logger.NewLogger(nil, false)
	sb := NewSyncBuilder()

	syncs, err := sb.SyncsFromConfig([]sync.SourceConfig{
		{
			URI:                "grpc://host:port",
			Provider:           syncProviderGrpc,
			IncrementalUpdates: true,
		},
	}, lg)
	require.NoError(t, err)
	require.Len(t, syncs, 1)
	grpcSync, ok := syncs[0].(*flagdgrpc.Sync)
	require.True(t, ok)
	require.True(t, grpcSync.IncrementalUpdates, "IncrementalUpdates should be propagated from SourceConfig to flagdgrpc.Sync")
}

// TestResolve pins the factory and the fetch triggers each provider name resolves to.
func TestResolve(t *testing.T) {
	t.Setenv("AZURE_STORAGE_ACCOUNT", "account")
	lg := logger.NewLogger(nil, false)

	tests := []struct {
		name         string
		config       sync.SourceConfig
		k8s          bool // inside Kubernetes
		wantFactory  string
		wantTriggers []string
		wantInterval uint32
		wantSource   string
	}{
		{"file outside Kubernetes", sync.SourceConfig{URI: "/tmp/f.json", Provider: "file"}, false, "file", []string{"fileinfo"}, 0, "/tmp/f.json"},
		{"file inside Kubernetes", sync.SourceConfig{URI: "/tmp/f.json", Provider: "file"}, true, "file", []string{"fsnotify"}, 0, "/tmp/f.json"},
		{"fsnotify is the file provider with that trigger", sync.SourceConfig{URI: "/tmp/f.json", Provider: "fsnotify"}, false, "file", []string{"fsnotify"}, 0, "/tmp/f.json"},
		{"fileinfo is the file provider with that trigger", sync.SourceConfig{URI: "/tmp/f.json", Provider: "fileinfo"}, true, "file", []string{"fileinfo"}, 0, "/tmp/f.json"},
		{"the file: prefix is stripped", sync.SourceConfig{URI: "file:/tmp/f.json", Provider: "file"}, false, "file", []string{"fileinfo"}, 0, "/tmp/f.json"},
		{"http polls", sync.SourceConfig{URI: "http://host/f", Provider: "http", Interval: 30}, false, "http", []string{"poller"}, 30, "http://host/f"},
		{"http polls on the default interval", sync.SourceConfig{URI: "http://host/f", Provider: "http"}, false, "http", []string{"poller"}, 5, "http://host/f"},
		{"gcs polls", sync.SourceConfig{URI: "gs://b/o", Provider: "gcs"}, false, "gcs", []string{"poller"}, 5, "gs://b/o"},
		{"azblob polls", sync.SourceConfig{URI: "azblob://b/o", Provider: "azblob"}, false, "azblob", []string{"poller"}, 5, "azblob://b/o"},
		{"s3 polls", sync.SourceConfig{URI: "s3://b/o", Provider: "s3", Interval: 10}, false, "s3", []string{"poller"}, 10, "s3://b/o"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.k8s {
				t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
			} else {
				t.Setenv("KUBERNETES_SERVICE_HOST", "")
			}
			res, err := NewSyncBuilder().Resolve(tt.config, lg)
			require.NoError(t, err)

			require.Equal(t, tt.wantFactory, res.Factory.Type())
			var triggers []string
			for _, tc := range res.Triggers {
				triggers = append(triggers, tc.Type)
			}
			require.Equal(t, tt.wantTriggers, triggers)
			require.Equal(t, tt.wantInterval, res.Interval)
			require.Equal(t, tt.wantSource, res.Source.URI)
			require.NotNil(t, res.Fetcher)
			require.Nil(t, res.Streaming)
		})
	}

	t.Run("a streaming provider has no fetcher and no triggers", func(t *testing.T) {
		res, err := NewSyncBuilder().Resolve(sync.SourceConfig{URI: "host:8013", Provider: "grpc"}, lg)
		require.NoError(t, err)
		require.Equal(t, "grpc", res.Factory.Type())
		require.Nil(t, res.Fetcher)
		require.Empty(t, res.Triggers)
		require.NotNil(t, res.Streaming)
	})

	t.Run("an interval beyond the maximum is refused for a polling provider only", func(t *testing.T) {
		_, err := NewSyncBuilder().Resolve(sync.SourceConfig{URI: "http://h/f", Provider: "http", Interval: 86401}, lg)
		require.EqualError(t, err, "invalid http sync configuration: polling interval 86401s exceeds maximum 86400s")
		_, err = NewSyncBuilder().Resolve(sync.SourceConfig{URI: "/tmp/f.json", Provider: "file", Interval: 86401}, lg)
		require.NoError(t, err)
	})
}
