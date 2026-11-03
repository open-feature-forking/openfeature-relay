module github.com/open-feature/flagd/test/inprocess-example/filegrpc

go 1.27

require (
	buf.build/gen/go/open-feature/flagd/grpc/go v1.6.1-20260217192757-1388a552fc3c.1
	buf.build/gen/go/open-feature/flagd/protocolbuffers/go v1.36.11-20260217192757-1388a552fc3c.1
	github.com/open-feature-forking/openfeature-relay/configformat/flagd v0.1.0
	github.com/open-feature-forking/openfeature-relay/core v0.20.0
	github.com/open-feature-forking/openfeature-relay/evaluator/flagd v0.1.0
	github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc v0.1.0
	google.golang.org/grpc v1.84.0
)

require (
	github.com/diegoholiveira/jsonlogic/v3 v3.9.1 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/open-feature/flagd-schemas v0.2.15 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/twmb/murmur3 v1.1.8 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace (
	github.com/open-feature-forking/openfeature-relay/configformat/flagd => ../../../configformat/flagd
	github.com/open-feature-forking/openfeature-relay/core => ../../../core
	github.com/open-feature-forking/openfeature-relay/evaluator/flagd => ../../../evaluator/flagd
	github.com/open-feature-forking/openfeature-relay/sync/azblob => ../../../sync/azblob
	github.com/open-feature-forking/openfeature-relay/sync/builder => ../../../sync/builder
	github.com/open-feature-forking/openfeature-relay/sync/file => ../../../sync/file
	github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc => ../../../sync/flagd-grpc
	github.com/open-feature-forking/openfeature-relay/sync/gcs => ../../../sync/gcs
	github.com/open-feature-forking/openfeature-relay/sync/http => ../../../sync/http
	github.com/open-feature-forking/openfeature-relay/sync/openfeature-operator => ../../../sync/openfeature-operator
	github.com/open-feature-forking/openfeature-relay/sync/s3 => ../../../sync/s3
	github.com/open-feature-forking/openfeature-relay/telemetry/otel => ../../../telemetry/otel
)
