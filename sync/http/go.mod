module github.com/open-feature-forking/openfeature-relay/sync/http

go 1.26.0

require github.com/open-feature-forking/openfeature-relay/core v0.20.0

require (
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/stretchr/testify v1.12.1
	go.uber.org/mock v0.5.2
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/oauth2 v0.37.0
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/open-feature-forking/openfeature-relay/core => ../../core
	github.com/open-feature-forking/openfeature-relay/sync/azblob => ../azblob
	github.com/open-feature-forking/openfeature-relay/sync/blob => ../blob
	github.com/open-feature-forking/openfeature-relay/sync/builder => ../builder
	github.com/open-feature-forking/openfeature-relay/sync/file => ../file
	github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc => ../flagd-grpc
	github.com/open-feature-forking/openfeature-relay/sync/gcs => ../gcs
	github.com/open-feature-forking/openfeature-relay/sync/openfeature-operator => ../openfeature-operator
	github.com/open-feature-forking/openfeature-relay/sync/s3 => ../s3
	github.com/open-feature-forking/openfeature-relay/telemetry/otel => ../../telemetry/otel
)
