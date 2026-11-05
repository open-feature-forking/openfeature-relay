package flagdgrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

const (
	// keepAliveTimeout is how long a ping may stay unanswered, as the Go flagd provider sets it.
	keepAliveTimeout = 5 * time.Second
	// retryMaxAttempts is the attempts the retry policy allows for a call, as the Go flagd provider sets it.
	retryMaxAttempts = 4
	// syncService is the service the retry policy applies to.
	syncService = "flagd.sync.v1.FlagSyncService"
)

// retryPolicy is the retry policy of the service config. Its field names are the ones gRPC's service config reads,
// spelled as the Go flagd provider spells them.
type retryPolicy struct {
	MaxAttempts          int      `json:"MaxAttempts"`
	InitialBackoff       string   `json:"InitialBackoff"`
	MaxBackoff           string   `json:"MaxBackoff"`
	BackoffMultiplier    float64  `json:"BackoffMultiplier"`
	RetryableStatusCodes []string `json:"RetryableStatusCodes"`
}

// retryServiceConfig is the service config of the Go flagd provider's retry policy for the sync service: up to four
// attempts on UNKNOWN and UNAVAILABLE, from initial up to largest, doubling. A zero delay takes the provider's default.
func retryServiceConfig(initial, largest time.Duration) string {
	if initial <= 0 {
		initial = defaultRetryBackoff
	}
	if largest <= 0 {
		largest = defaultRetryBackoffMax
	}
	// gRPC reads seconds only, such as "1s" and "0.1s", never a compound like "2m0s"
	policy := map[string]any{
		"methodConfig": []map[string]any{{
			"name": []map[string]string{{"service": syncService}},
			"retryPolicy": retryPolicy{
				MaxAttempts:          retryMaxAttempts,
				InitialBackoff:       fmt.Sprint(initial.Seconds()) + "s",
				MaxBackoff:           fmt.Sprint(largest.Seconds()) + "s",
				BackoffMultiplier:    2.0,
				RetryableStatusCodes: []string{"UNKNOWN", "UNAVAILABLE"},
			},
		}},
	}
	out, _ := json.Marshal(policy)
	return string(out)
}

// retryOptedIn reports whether the source asked for the provider's retry policy.
func (o Options) retryOptedIn() bool { return o.RetryBackoff != 0 || o.RetryBackoffMax != 0 }

// keepAliveParameters are the parameters of the keepalive the source asked for, and whether it asked.
func (o Options) keepAliveParameters() (keepalive.ClientParameters, bool) {
	if o.KeepAliveTime <= 0 {
		return keepalive.ClientParameters{}, false
	}
	return keepalive.ClientParameters{Time: o.KeepAliveTime, Timeout: keepAliveTimeout, PermitWithoutStream: true}, true
}

// connectionOptions are the dial options the opt-in settings add to a connection the module dials itself: the retry
// service config and the keepalive. Without settings it adds none.
func (o Options) connectionOptions() []grpc.DialOption {
	var opts []grpc.DialOption
	if o.retryOptedIn() {
		opts = append(opts, grpc.WithDefaultServiceConfig(retryServiceConfig(o.RetryBackoff, o.RetryBackoffMax)))
	}
	if params, ok := o.keepAliveParameters(); ok {
		opts = append(opts, grpc.WithKeepaliveParams(params))
	}
	return opts
}

// selectorOptions are the dial options that send the selector as the flagd-selector header on every call, besides the
// request body. They apply when the source asked for the header and has a selector.
func (o Options) selectorOptions(selector string) []grpc.DialOption {
	if !o.SelectorHeader || selector == "" {
		return nil
	}
	return []grpc.DialOption{
		grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
			invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
		) error {
			return invoker(metadata.AppendToOutgoingContext(ctx, selectorHeader, selector), method, req, reply, cc, opts...)
		}),
		grpc.WithChainStreamInterceptor(func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
			streamer grpc.Streamer, opts ...grpc.CallOption,
		) (grpc.ClientStream, error) {
			return streamer(metadata.AppendToOutgoingContext(ctx, selectorHeader, selector), desc, cc, method, opts...)
		}),
	}
}
