module github.com/open-feature/flagd/test/telemetry-example

go 1.27

require (
	github.com/open-feature-forking/openfeature-relay/core v0.20.0
	github.com/stretchr/testify v1.12.1
)

require (
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)

replace github.com/open-feature-forking/openfeature-relay/core => ../../core
