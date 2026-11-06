module github.com/open-feature-forking/openfeature-relay/service/event-collector

go 1.27

require (
	github.com/open-feature-forking/openfeature-relay/core v0.20.0
	github.com/open-feature-forking/openfeature-relay/service/transport v0.1.0
	github.com/stretchr/testify v1.12.1
	go.uber.org/zap v1.27.0
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/rs/cors v1.11.1 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace (
	github.com/open-feature-forking/openfeature-relay/configformat/flagd => ../../configformat/flagd
	github.com/open-feature-forking/openfeature-relay/core => ../../core
	github.com/open-feature-forking/openfeature-relay/evaluator/flagd => ../../evaluator/flagd
	github.com/open-feature-forking/openfeature-relay/runtime => ../../runtime
	github.com/open-feature-forking/openfeature-relay/service/flag-evaluation => ../flag-evaluation
	github.com/open-feature-forking/openfeature-relay/service/flag-evaluation-legacy => ../flag-evaluation-legacy
	github.com/open-feature-forking/openfeature-relay/service/flag-sync => ../flag-sync
	github.com/open-feature-forking/openfeature-relay/service/transport => ../transport
	github.com/open-feature-forking/openfeature-relay/sync/azblob => ../../sync/azblob
	github.com/open-feature-forking/openfeature-relay/sync/builder => ../../sync/builder
	github.com/open-feature-forking/openfeature-relay/sync/file => ../../sync/file
	github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc => ../../sync/flagd-grpc
	github.com/open-feature-forking/openfeature-relay/sync/gcs => ../../sync/gcs
	github.com/open-feature-forking/openfeature-relay/sync/http => ../../sync/http
	github.com/open-feature-forking/openfeature-relay/sync/openfeature-operator => ../../sync/openfeature-operator
	github.com/open-feature-forking/openfeature-relay/sync/s3 => ../../sync/s3
	github.com/open-feature-forking/openfeature-relay/telemetry/otel => ../../telemetry/otel
)
