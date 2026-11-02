package telemetry

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

// GRPCClientDialOption instruments an outbound gRPC client connection, such as the flagd to flag-server
// sync channel, so that the standard rpc.client.* metrics land on the same reader as flagd's other
// metrics. grpc-go supports several stats handlers, so it composes with any a caller installs itself.
func GRPCClientDialOption() grpc.DialOption {
	return grpc.WithStatsHandler(otelgrpc.NewClientHandler())
}
