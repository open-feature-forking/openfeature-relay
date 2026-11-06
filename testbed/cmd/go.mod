module github.com/open-feature-forking/openfeature-relay/testbed/cmd

go 1.27

require (
	github.com/open-feature-forking/openfeature-relay/configformat/flagd v0.1.0
	github.com/open-feature-forking/openfeature-relay/core v0.20.0
	github.com/open-feature-forking/openfeature-relay/evaluator/flagd v0.1.0
	github.com/open-feature-forking/openfeature-relay/runtime v0.0.0
	github.com/open-feature-forking/openfeature-relay/service/flag-evaluation v0.1.0
	github.com/open-feature-forking/openfeature-relay/service/flag-evaluation-legacy v0.1.0
	github.com/open-feature-forking/openfeature-relay/service/flag-sync v0.1.0
	github.com/open-feature-forking/openfeature-relay/service/transport v0.1.0
	github.com/open-feature-forking/openfeature-relay/sync/file v0.1.0
	github.com/open-feature-forking/openfeature-relay/testbed/control v0.0.0
	github.com/open-feature-forking/openfeature-relay/testbed/source v0.0.0
	github.com/stretchr/testify v1.12.1
	go.uber.org/zap v1.27.0
)

require (
	buf.build/gen/go/open-feature/flagd/connectrpc/go v1.19.1-20260217192757-1388a552fc3c.2 // indirect
	buf.build/gen/go/open-feature/flagd/protocolbuffers/go v1.36.11-20260217192757-1388a552fc3c.1 // indirect
	connectrpc.com/connect v1.19.1 // indirect
	github.com/common-nighthawk/go-figure v0.0.0-20210622060536-734e95fb86be // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.6 // indirect
	github.com/diegoholiveira/jsonlogic/v3 v3.9.1 // indirect
	github.com/dimiro1/banner v1.1.0 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/open-feature-forking/openfeature-relay/eventsink/webhook v0.0.0-00010101000000-000000000000 // indirect
	github.com/open-feature-forking/openfeature-relay/service/event-collector v0.0.0-00010101000000-000000000000 // indirect
	github.com/open-feature-forking/openfeature-relay/sync/builder v0.1.0 // indirect
	github.com/open-feature/flagd-schemas v0.2.15 // indirect
	github.com/pelletier/go-toml/v2 v2.2.3 // indirect
	github.com/rs/cors v1.11.1 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/sagikazarmark/locafero v0.7.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/sourcegraph/conc v0.3.0 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/spf13/cobra v1.9.1 // indirect
	github.com/spf13/pflag v1.0.6 // indirect
	github.com/spf13/viper v1.20.1 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/twmb/murmur3 v1.1.8 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/grpc v1.84.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/open-feature-forking/openfeature-relay/configformat/flagd => ../../configformat/flagd
	github.com/open-feature-forking/openfeature-relay/core => ../../core
	github.com/open-feature-forking/openfeature-relay/evaluator/flagd => ../../evaluator/flagd
	github.com/open-feature-forking/openfeature-relay/eventsink/webhook => ../../eventsink/webhook
	github.com/open-feature-forking/openfeature-relay/runtime => ../../runtime
	github.com/open-feature-forking/openfeature-relay/service/event-collector => ../../service/event-collector
	github.com/open-feature-forking/openfeature-relay/service/flag-evaluation => ../../service/flag-evaluation
	github.com/open-feature-forking/openfeature-relay/service/flag-evaluation-legacy => ../../service/flag-evaluation-legacy
	github.com/open-feature-forking/openfeature-relay/service/flag-sync => ../../service/flag-sync
	github.com/open-feature-forking/openfeature-relay/service/ofrep => ../../service/ofrep
	github.com/open-feature-forking/openfeature-relay/service/transport => ../../service/transport
	github.com/open-feature-forking/openfeature-relay/sync/azblob => ../../sync/azblob
	github.com/open-feature-forking/openfeature-relay/sync/builder => ../../sync/builder
	github.com/open-feature-forking/openfeature-relay/sync/file => ../../sync/file
	github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc => ../../sync/flagd-grpc
	github.com/open-feature-forking/openfeature-relay/sync/gcs => ../../sync/gcs
	github.com/open-feature-forking/openfeature-relay/sync/http => ../../sync/http
	github.com/open-feature-forking/openfeature-relay/sync/openfeature-operator => ../../sync/openfeature-operator
	github.com/open-feature-forking/openfeature-relay/sync/s3 => ../../sync/s3
	github.com/open-feature-forking/openfeature-relay/telemetry/otel => ../../telemetry/otel
	github.com/open-feature-forking/openfeature-relay/testbed/control => ../control
	github.com/open-feature-forking/openfeature-relay/testbed/source => ../source
	github.com/open-feature/flagd/flagd => ../../flagd
)
