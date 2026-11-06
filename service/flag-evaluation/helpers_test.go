package service

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/open-feature-forking/openfeature-relay/telemetry/otel/oteltest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"google.golang.org/protobuf/types/known/structpb"
)

var metadata = map[string]interface{}{
	"scope": "some-scope",
}

var responseStruct *structpb.Struct

func init() {
	pbStruct, err := structpb.NewStruct(metadata)
	if err != nil {
		panic("failure to generate protobuf structure from metadata")
	}

	responseStruct = pbStruct
}

func getMetricReader() (telemetry.Provider, metric.Reader) {
	exp := metric.NewManualReader()
	return telemetrytest.ProviderFor(exp), exp
}

const invalidSelectorExpr = "invalidKey=val"

type invalidSelectorCase struct {
	name string
	call func() error
}

func assertInvalidSelectorConnect(t *testing.T, cases []invalidSelectorCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			require.Error(t, err)
			var connectErr *connect.Error
			require.True(t, errors.As(err, &connectErr))
			require.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
		})
	}
}
