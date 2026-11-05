module github.com/open-feature-forking/openfeature-relay/test/concurrency

go 1.27

require (
	buf.build/gen/go/open-feature/flagd/grpc/go v1.6.2-20260824202443-4824facda674.1
	buf.build/gen/go/open-feature/flagd/protocolbuffers/go v1.36.11-20260824202443-4824facda674.1
	github.com/stretchr/testify v1.12.1
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
)

require (
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260817212433-ac3dfec99bb1 // indirect
)
