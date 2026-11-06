module github.com/open-feature-forking/openfeature-relay/service/ofrep

go 1.27

require (
	github.com/gorilla/mux v1.8.1
	github.com/launchdarkly/eventsource v1.11.0
	github.com/open-feature-forking/openfeature-relay/core v0.20.0
	github.com/open-feature-forking/openfeature-relay/evaluator/flagd v0.1.0
	github.com/open-feature-forking/openfeature-relay/service/transport v0.1.0
	github.com/open-feature-forking/openfeature-relay/telemetry/otel v0.1.0
	github.com/rs/xid v1.6.0
	github.com/stretchr/testify v1.12.1
	go.opentelemetry.io/otel v1.47.0
	go.opentelemetry.io/otel/sdk v1.47.0
	go.opentelemetry.io/otel/sdk/metric v1.47.0
	go.uber.org/mock v0.5.2
	go.uber.org/zap v1.27.0
	golang.org/x/sync v0.24.0
)

require (
	connectrpc.com/connect v1.19.1 // indirect
	connectrpc.com/otelconnect v0.7.2 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/diegoholiveira/jsonlogic/v3 v3.9.1 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.31.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/klauspost/compress v1.19.1 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/open-feature-forking/openfeature-relay/configformat/flagd v0.1.0 // indirect
	github.com/open-feature/flagd-schemas v0.2.15 // indirect
	github.com/prometheus/client_golang v1.24.1 // indirect
	github.com/prometheus/client_model v0.6.3 // indirect
	github.com/prometheus/common v0.72.0 // indirect
	github.com/prometheus/otlptranslator v1.0.0 // indirect
	github.com/prometheus/procfs v0.22.0 // indirect
	github.com/rs/cors v1.11.1 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/twmb/murmur3 v1.1.8 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/bridges/prometheus v0.72.0 // indirect
	go.opentelemetry.io/contrib/exporters/autoexport v0.72.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.72.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.72.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.23.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.23.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/prometheus v0.69.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdoutlog v0.23.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdoutmetric v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk/log v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.1 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/grpc v1.84.0 // indirect
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
