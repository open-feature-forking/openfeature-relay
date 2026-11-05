module github.com/open-feature-forking/openfeature-relay/test/concurrency

go 1.27

require (
	buf.build/gen/go/open-feature/flagd/protocolbuffers/go v1.36.11-20260217192757-1388a552fc3c.1
	github.com/stretchr/testify v1.12.1
	google.golang.org/protobuf v1.36.12
)

require go.yaml.in/yaml/v3 v3.0.5 // indirect
