package service

import (
	"fmt"

	evalV2 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v2"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"
)

type response[T constraints] interface {
	SetResult(value T, variant, reason string, metadata map[string]interface{}) error
}

type constraints interface {
	bool | string | map[string]any | float64 | int64
}

// V2 response types with optional value and variant

type responseV2[T constraints] interface {
	response[T]
	SetReasonOnly(reason string, metadata map[string]interface{}) error
}

// compile-time interface satisfaction checks
var (
	_ responseV2[bool]           = (*booleanResponseV2)(nil)
	_ responseV2[string]         = (*stringResponseV2)(nil)
	_ responseV2[float64]        = (*floatResponseV2)(nil)
	_ responseV2[int64]          = (*intResponseV2)(nil)
	_ responseV2[map[string]any] = (*objectResponseV2)(nil)
)

type booleanResponseV2 struct {
	evalV2Resp *connect.Response[evalV2.ResolveBooleanResponse]
}

func (r *booleanResponseV2) SetResult(value bool, variant, reason string, metadata map[string]interface{}) error {
	newStruct, err := structpb.NewStruct(metadata)
	if err != nil {
		return fmt.Errorf("failure to wrap metadata %w", err)
	}

	if r.evalV2Resp != nil {
		r.evalV2Resp.Msg.Value = &value
		if variant != "" {
			r.evalV2Resp.Msg.Variant = &variant
		}
		r.evalV2Resp.Msg.Reason = reason
		r.evalV2Resp.Msg.Metadata = newStruct
	}

	return nil
}

func (r *booleanResponseV2) SetReasonOnly(reason string, metadata map[string]interface{}) error {
	newStruct, err := structpb.NewStruct(metadata)
	if err != nil {
		return fmt.Errorf("failure to wrap metadata %w", err)
	}

	if r.evalV2Resp != nil {
		// Leave Value and Variant as nil (unset)
		r.evalV2Resp.Msg.Reason = reason
		r.evalV2Resp.Msg.Metadata = newStruct
	}

	return nil
}

type stringResponseV2 struct {
	evalV2Resp *connect.Response[evalV2.ResolveStringResponse]
}

func (r *stringResponseV2) SetResult(value string, variant, reason string, metadata map[string]interface{}) error {
	newStruct, err := structpb.NewStruct(metadata)
	if err != nil {
		return fmt.Errorf("failure to wrap metadata %w", err)
	}

	if r.evalV2Resp != nil {
		r.evalV2Resp.Msg.Value = &value
		if variant != "" {
			r.evalV2Resp.Msg.Variant = &variant
		}
		r.evalV2Resp.Msg.Reason = reason
		r.evalV2Resp.Msg.Metadata = newStruct
	}

	return nil
}

func (r *stringResponseV2) SetReasonOnly(reason string, metadata map[string]interface{}) error {
	newStruct, err := structpb.NewStruct(metadata)
	if err != nil {
		return fmt.Errorf("failure to wrap metadata %w", err)
	}

	if r.evalV2Resp != nil {
		// Leave Value and Variant as nil (unset)
		r.evalV2Resp.Msg.Reason = reason
		r.evalV2Resp.Msg.Metadata = newStruct
	}

	return nil
}

type floatResponseV2 struct {
	evalV2Resp *connect.Response[evalV2.ResolveFloatResponse]
}

func (r *floatResponseV2) SetResult(value float64, variant, reason string, metadata map[string]interface{}) error {
	newStruct, err := structpb.NewStruct(metadata)
	if err != nil {
		return fmt.Errorf("failure to wrap metadata %w", err)
	}

	if r.evalV2Resp != nil {
		r.evalV2Resp.Msg.Value = &value
		if variant != "" {
			r.evalV2Resp.Msg.Variant = &variant
		}
		r.evalV2Resp.Msg.Reason = reason
		r.evalV2Resp.Msg.Metadata = newStruct
	}

	return nil
}

func (r *floatResponseV2) SetReasonOnly(reason string, metadata map[string]interface{}) error {
	newStruct, err := structpb.NewStruct(metadata)
	if err != nil {
		return fmt.Errorf("failure to wrap metadata %w", err)
	}

	if r.evalV2Resp != nil {
		// Leave Value and Variant as nil (unset)
		r.evalV2Resp.Msg.Reason = reason
		r.evalV2Resp.Msg.Metadata = newStruct
	}

	return nil
}

type intResponseV2 struct {
	evalV2Resp *connect.Response[evalV2.ResolveIntResponse]
}

func (r *intResponseV2) SetResult(value int64, variant, reason string, metadata map[string]interface{}) error {
	newStruct, err := structpb.NewStruct(metadata)
	if err != nil {
		return fmt.Errorf("failure to wrap metadata %w", err)
	}

	if r.evalV2Resp != nil {
		r.evalV2Resp.Msg.Value = &value
		if variant != "" {
			r.evalV2Resp.Msg.Variant = &variant
		}
		r.evalV2Resp.Msg.Reason = reason
		r.evalV2Resp.Msg.Metadata = newStruct
	}
	return nil
}

func (r *intResponseV2) SetReasonOnly(reason string, metadata map[string]interface{}) error {
	newStruct, err := structpb.NewStruct(metadata)
	if err != nil {
		return fmt.Errorf("failure to wrap metadata %w", err)
	}

	if r.evalV2Resp != nil {
		// Leave Value and Variant as nil (unset)
		r.evalV2Resp.Msg.Reason = reason
		r.evalV2Resp.Msg.Metadata = newStruct
	}
	return nil
}

type objectResponseV2 struct {
	evalV2Resp *connect.Response[evalV2.ResolveObjectResponse]
}

func (r *objectResponseV2) SetResult(value map[string]any, variant, reason string,
	metadata map[string]interface{},
) error {
	newStruct, err := structpb.NewStruct(metadata)
	if err != nil {
		return fmt.Errorf("failure to wrap metadata %w", err)
	}
	if r.evalV2Resp != nil {
		r.evalV2Resp.Msg.Reason = reason
		val, err := structpb.NewStruct(value)
		if err != nil {
			return fmt.Errorf("struct response construction: %w", err)
		}

		r.evalV2Resp.Msg.Value = val
		if variant != "" {
			r.evalV2Resp.Msg.Variant = &variant
		}
		r.evalV2Resp.Msg.Metadata = newStruct
	}
	return nil
}

func (r *objectResponseV2) SetReasonOnly(reason string, metadata map[string]interface{}) error {
	newStruct, err := structpb.NewStruct(metadata)
	if err != nil {
		return fmt.Errorf("failure to wrap metadata %w", err)
	}
	if r.evalV2Resp != nil {
		// Leave Value and Variant as nil (unset)
		r.evalV2Resp.Msg.Reason = reason
		r.evalV2Resp.Msg.Metadata = newStruct
	}
	return nil
}
