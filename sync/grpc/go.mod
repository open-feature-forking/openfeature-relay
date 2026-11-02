module github.com/open-feature-forking/openfeature-relay/sync/grpc

go 1.26.0

require github.com/open-feature-forking/openfeature-relay/core v0.20.0

require (
	buf.build/gen/go/open-feature/flagd/grpc/go v1.6.1-20260217192757-1388a552fc3c.1
	buf.build/gen/go/open-feature/flagd/protocolbuffers/go v1.36.11-20260217192757-1388a552fc3c.1
	github.com/stretchr/testify v1.12.1
	go.uber.org/mock v0.5.2
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sync v0.23.0
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

replace (
	github.com/open-feature-forking/openfeature-relay/core => ../../core
	github.com/open-feature-forking/openfeature-relay/sync/azblob => ../azblob
	github.com/open-feature-forking/openfeature-relay/sync/blob => ../blob
	github.com/open-feature-forking/openfeature-relay/sync/builder => ../builder
	github.com/open-feature-forking/openfeature-relay/sync/file => ../file
	github.com/open-feature-forking/openfeature-relay/sync/gcs => ../gcs
	github.com/open-feature-forking/openfeature-relay/sync/http => ../http
	github.com/open-feature-forking/openfeature-relay/sync/kubernetes => ../kubernetes
	github.com/open-feature-forking/openfeature-relay/sync/s3 => ../s3
	github.com/open-feature-forking/openfeature-relay/telemetry/otel => ../../telemetry/otel
)
