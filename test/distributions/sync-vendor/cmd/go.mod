module github.com/open-feature-forking/openfeature-relay/test/distributions/sync-vendor/cmd

go 1.27

require (
	github.com/open-feature-forking/openfeature-relay/core v0.0.0
	github.com/open-feature-forking/openfeature-relay/runtime v0.0.0
)

replace (
	github.com/open-feature-forking/openfeature-relay/core => ../../../../core
	github.com/open-feature-forking/openfeature-relay/runtime => ../../../../runtime
)
