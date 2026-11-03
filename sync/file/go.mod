module github.com/open-feature-forking/openfeature-relay/sync/file

go 1.26.0

require (
	github.com/open-feature-forking/openfeature-relay/core v0.20.0
	github.com/stretchr/testify v1.12.1
)

require go.yaml.in/yaml/v3 v3.0.5 // indirect

require (
	github.com/fsnotify/fsnotify v1.9.0
	github.com/google/go-cmp v0.7.0
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0
	golang.org/x/sys v0.48.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/open-feature-forking/openfeature-relay/configformat/flagd => ../../configformat/flagd
	github.com/open-feature-forking/openfeature-relay/core => ../../core
	github.com/open-feature-forking/openfeature-relay/evaluator/flagd => ../../evaluator/flagd
	github.com/open-feature-forking/openfeature-relay/sync/azblob => ../azblob
	github.com/open-feature-forking/openfeature-relay/sync/builder => ../builder
	github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc => ../flagd-grpc
	github.com/open-feature-forking/openfeature-relay/sync/gcs => ../gcs
	github.com/open-feature-forking/openfeature-relay/sync/http => ../http
	github.com/open-feature-forking/openfeature-relay/sync/openfeature-operator => ../openfeature-operator
	github.com/open-feature-forking/openfeature-relay/sync/s3 => ../s3
	github.com/open-feature-forking/openfeature-relay/telemetry/otel => ../../telemetry/otel
)
