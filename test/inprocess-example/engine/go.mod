module github.com/open-feature/flagd/test/inprocess-example/engine

go 1.27

require (
	github.com/open-feature-forking/openfeature-relay/core v0.20.0
	github.com/open-feature/flagd/test/configformat-example v0.0.0
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/open-feature/flagd/test/configformat-example => ../../configformat-example

replace github.com/open-feature/flagd/flagd => ../../../flagd

replace (
	github.com/open-feature-forking/openfeature-relay/configformat/flagd => ../../../configformat/flagd
	github.com/open-feature-forking/openfeature-relay/core => ../../../core
	github.com/open-feature-forking/openfeature-relay/evaluator/flagd => ../../../evaluator/flagd
	github.com/open-feature-forking/openfeature-relay/sync/azblob => ../../../sync/azblob
	github.com/open-feature-forking/openfeature-relay/sync/blob => ../../../sync/blob
	github.com/open-feature-forking/openfeature-relay/sync/builder => ../../../sync/builder
	github.com/open-feature-forking/openfeature-relay/sync/file => ../../../sync/file
	github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc => ../../../sync/flagd-grpc
	github.com/open-feature-forking/openfeature-relay/sync/gcs => ../../../sync/gcs
	github.com/open-feature-forking/openfeature-relay/sync/http => ../../../sync/http
	github.com/open-feature-forking/openfeature-relay/sync/openfeature-operator => ../../../sync/openfeature-operator
	github.com/open-feature-forking/openfeature-relay/sync/s3 => ../../../sync/s3
	github.com/open-feature-forking/openfeature-relay/telemetry/otel => ../../../telemetry/otel
)

replace github.com/open-feature-forking/openfeature-relay/runtime => ../../../runtime

replace github.com/open-feature-forking/openfeature-relay/service/transport => ../../../service/transport

replace github.com/open-feature-forking/openfeature-relay/service/flag-evaluation => ../../../service/flag-evaluation

replace github.com/open-feature-forking/openfeature-relay/service/ofrep => ../../../service/ofrep

replace github.com/open-feature-forking/openfeature-relay/service/flag-sync => ../../../service/flag-sync
