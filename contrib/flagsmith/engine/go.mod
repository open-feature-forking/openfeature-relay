module github.com/open-feature-forking/openfeature-relay/contrib/flagsmith/engine

go 1.26.0

require (
	github.com/Flagsmith/flagsmith-go-client/v5 v5.3.0
	github.com/itlightning/dateparse v0.2.1
	github.com/open-feature-forking/openfeature-relay/core v0.20.0
	github.com/stretchr/testify v1.12.1
)

require (
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/ohler55/ojg v1.28.1 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)

replace github.com/open-feature-forking/openfeature-relay/core => ../../../core
