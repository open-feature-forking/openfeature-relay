package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	evalV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	flagdService "github.com/open-feature/flagd/flagd/pkg/service"
	"github.com/rs/xid"
	"google.golang.org/protobuf/types/known/structpb"
)

type FlagEvaluationService struct {
	logger                     *logger.Logger
	eval                       evaluator.IResolver
	metrics                    *flagdService.Metrics
	eventingConfiguration      IEvents
	flagEvalTracer             telemetry.Tracer
	contextValues              map[string]any
	headerToContextKeyMappings map[string]string
	deadline                   time.Duration
}

// NewFlagEvaluationService creates a FlagEvaluationService with provided parameters
func NewFlagEvaluationService(log *logger.Logger,
	eval evaluator.IResolver,
	eventingCfg IEvents,
	tp telemetry.Provider,
	contextValues map[string]any,
	headerToContextKeyMappings map[string]string,
	streamDeadline time.Duration,
) *FlagEvaluationService {
	svc := &FlagEvaluationService{
		logger:                     log,
		eval:                       eval,
		metrics:                    flagdService.NewMetrics(telemetry.OrNoop(tp).Meter(flagdService.MetricsScope)),
		eventingConfiguration:      eventingCfg,
		flagEvalTracer:             telemetry.OrNoop(tp).Tracer("flagd.evaluation.v1"),
		contextValues:              contextValues,
		headerToContextKeyMappings: headerToContextKeyMappings,
		deadline:                   streamDeadline,
	}

	return svc
}

// nolint:dupl,funlen
func (s *FlagEvaluationService) ResolveAll(
	ctx context.Context,
	req *connect.Request[evalV1.ResolveAllRequest],
) (*connect.Response[evalV1.ResolveAllResponse], error) {
	reqID := xid.New().String()
	defer s.logger.ClearFields(reqID)

	ctx, span := s.flagEvalTracer.Start(ctx, "resolveAll", telemetry.WithSpanKind(telemetry.SpanKindServer))
	defer span.End()

	res := &evalV1.ResolveAllResponse{
		Flags: make(map[string]*evalV1.AnyFlag),
	}

	selector, err := selectorFromHeader(ctx, s.logger, req.Header())
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	evaluationContext := mergeContexts(req.Msg.GetContext().AsMap(), s.contextValues, req.Header(), s.headerToContextKeyMappings)
	ctx = context.WithValue(ctx, store.SelectorContextKey{}, selector)
	ctx = context.WithValue(ctx, evaluator.ProtoVersionKey, "v1") //nolint:staticcheck // exported string key; changing type is breaking

	resolutions, flagSetMetadata, err := s.eval.ResolveAllValues(ctx, reqID, evaluationContext)
	if err != nil {
		s.logger.WarnWithID(reqID, fmt.Sprintf("error resolving all flags: %v", err))
		return nil, fmt.Errorf("error resolving flags. Tracking ID: %s", reqID)
	}

	span.SetAttributes(telemetry.Int("feature_flag.count", len(resolutions)))
	for _, resolved := range resolutions {
		// register the impression and reason for each flag evaluated
		s.metrics.RecordEvaluation(
			ctx, resolved.Error, resolved.Reason, resolved.Variant, resolved.FlagKey,
			resolved.Metadata,
		)
		switch v := resolved.Value.(type) {
		case bool:
			res.Flags[resolved.FlagKey] = &evalV1.AnyFlag{
				Reason:  resolved.Reason,
				Variant: resolved.Variant,
				Value: &evalV1.AnyFlag_BoolValue{
					BoolValue: v,
				},
			}
		case string:
			res.Flags[resolved.FlagKey] = &evalV1.AnyFlag{
				Reason:  resolved.Reason,
				Variant: resolved.Variant,
				Value: &evalV1.AnyFlag_StringValue{
					StringValue: v,
				},
			}
		case float64:
			res.Flags[resolved.FlagKey] = &evalV1.AnyFlag{
				Reason:  resolved.Reason,
				Variant: resolved.Variant,
				Value: &evalV1.AnyFlag_DoubleValue{
					DoubleValue: v,
				},
			}
		case map[string]any:
			val, err := structpb.NewStruct(v)
			if err != nil {
				s.logger.ErrorWithID(reqID, fmt.Sprintf("struct response construction: %v", err))
				continue
			}
			res.Flags[resolved.FlagKey] = &evalV1.AnyFlag{
				Reason:  resolved.Reason,
				Variant: resolved.Variant,
				Value: &evalV1.AnyFlag_ObjectValue{
					ObjectValue: val,
				},
			}
		}
		metadata, err := structpb.NewStruct(resolved.Metadata)
		if err != nil {
			s.logger.WarnWithID(reqID, fmt.Sprintf("error resolving all flags: %v", err))
			return nil, fmt.Errorf("error resolving flags. Tracking ID: %s", reqID)
		}

		res.Flags[resolved.FlagKey].Metadata = metadata
	}
	res.Metadata, err = structpb.NewStruct(flagSetMetadata)
	if err != nil {
		s.logger.WarnWithID(reqID, fmt.Sprintf("error resolving all flags: %v", err))
		return nil, fmt.Errorf("error resolving flags. Tracking ID: %s", reqID)
	}

	return connect.NewResponse(res), nil
}

// nolint: dupl
func (s *FlagEvaluationService) EventStream(
	ctx context.Context,
	req *connect.Request[evalV1.EventStreamRequest],
	stream *connect.ServerStream[evalV1.EventStreamResponse],
) error {
	// attach server-side stream deadline to context
	s.logger.Debug("starting event stream for request")

	if s.deadline != 0 {
		streamDeadline := time.Now().Add(s.deadline)
		deadlineCtx, cancel := context.WithDeadline(ctx, streamDeadline)
		ctx = deadlineCtx
		defer cancel()
	}

	s.logger.Debug("starting event stream for request")
	requestNotificationChan := make(chan service.Notification, 1)
	selector, err := selectorFromHeader(ctx, s.logger, req.Header())
	if err != nil {
		return err
	}
	s.eventingConfiguration.Subscribe(ctx, req, &selector, requestNotificationChan)
	defer s.eventingConfiguration.Unsubscribe(req)

	// send initial ready to the stream; the notifier channel may be closed by cleanup and this avoids racing with it
	if err := stream.Send(&evalV1.EventStreamResponse{Type: string(service.ProviderReady)}); err != nil {
		s.logger.Error(err.Error())
	}
	for {
		select {
		case <-time.After(20 * time.Second):
			err := stream.Send(&evalV1.EventStreamResponse{
				Type: string(service.KeepAlive),
			})
			if err != nil {
				s.logger.Error(err.Error())
			}
		case notification, open := <-requestNotificationChan:
			if !open {
				// the subscription ended with the store watch
				return nil
			}
			d, err := structpb.NewStruct(notification.Data)
			if err != nil {
				s.logger.Error(err.Error())
			}
			err = stream.Send(&evalV1.EventStreamResponse{
				Type: string(notification.Type),
				Data: d,
			})
			if err != nil {
				s.logger.Error(err.Error())
			}
		case <-ctx.Done():
			// a notification queued before the context ended, provider_shutdown for one, is still sent
			select {
			case notification, open := <-requestNotificationChan:
				if open {
					d, _ := structpb.NewStruct(notification.Data)
					_ = stream.Send(&evalV1.EventStreamResponse{Type: string(notification.Type), Data: d})
				}
			default:
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				s.logger.Debug(fmt.Sprintf("server-side deadline of %s exceeded, exiting stream request with grpc error code 4", s.deadline.String()))
				return connect.NewError(connect.CodeDeadlineExceeded, fmt.Errorf("%s", "stream closed due to server-side timeout"))
			}
			return nil
		}
	}
}

func (s *FlagEvaluationService) ResolveBoolean(
	ctx context.Context,
	req *connect.Request[evalV1.ResolveBooleanRequest],
) (*connect.Response[evalV1.ResolveBooleanResponse], error) {
	ctx, span := s.flagEvalTracer.Start(ctx, "resolveBoolean", telemetry.WithSpanKind(telemetry.SpanKindServer))
	defer span.End()

	selector, err := selectorFromHeader(ctx, s.logger, req.Header())
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	ctx = context.WithValue(ctx, store.SelectorContextKey{}, selector)
	ctx = context.WithValue(ctx, evaluator.ProtoVersionKey, "v1") //nolint:staticcheck // exported string key; changing type is breaking

	res := connect.NewResponse(&evalV1.ResolveBooleanResponse{})
	err = resolve(
		ctx,
		span,
		s.logger,
		s.eval.ResolveBooleanValue,
		req.Header(),
		req.Msg.GetFlagKey(),
		req.Msg.GetContext(),
		&booleanResponse{evalV1Resp: res},
		s.metrics,
		s.contextValues,
		s.headerToContextKeyMappings,
	)
	if err != nil {
		span.RecordError(err)
		span.SetError(fmt.Sprintf("error evaluating flag with key %s", req.Msg.GetFlagKey()))
	}

	return res, err
}

func (s *FlagEvaluationService) ResolveString(
	ctx context.Context,
	req *connect.Request[evalV1.ResolveStringRequest],
) (*connect.Response[evalV1.ResolveStringResponse], error) {
	ctx, span := s.flagEvalTracer.Start(ctx, "resolveString", telemetry.WithSpanKind(telemetry.SpanKindServer))
	defer span.End()

	selector, err := selectorFromHeader(ctx, s.logger, req.Header())
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	ctx = context.WithValue(ctx, store.SelectorContextKey{}, selector)
	ctx = context.WithValue(ctx, evaluator.ProtoVersionKey, "v1") //nolint:staticcheck // exported string key; changing type is breaking

	res := connect.NewResponse(&evalV1.ResolveStringResponse{})
	err = resolve(
		ctx,
		span,
		s.logger,
		s.eval.ResolveStringValue,
		req.Header(),
		req.Msg.GetFlagKey(),
		req.Msg.GetContext(),
		&stringResponse{evalV1Resp: res},
		s.metrics,
		s.contextValues,
		s.headerToContextKeyMappings,
	)
	if err != nil {
		span.RecordError(err)
		span.SetError(fmt.Sprintf("error evaluating flag with key %s", req.Msg.GetFlagKey()))
	}

	return res, err
}

func (s *FlagEvaluationService) ResolveInt(
	ctx context.Context,
	req *connect.Request[evalV1.ResolveIntRequest],
) (*connect.Response[evalV1.ResolveIntResponse], error) {
	ctx, span := s.flagEvalTracer.Start(ctx, "resolveInt", telemetry.WithSpanKind(telemetry.SpanKindServer))
	defer span.End()

	selector, err := selectorFromHeader(ctx, s.logger, req.Header())
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	ctx = context.WithValue(ctx, store.SelectorContextKey{}, selector)
	ctx = context.WithValue(ctx, evaluator.ProtoVersionKey, "v1") //nolint:staticcheck // exported string key; changing type is breaking

	res := connect.NewResponse(&evalV1.ResolveIntResponse{})
	err = resolve(
		ctx,
		span,
		s.logger,
		s.eval.ResolveIntValue,
		req.Header(),
		req.Msg.GetFlagKey(),
		req.Msg.GetContext(),
		&intResponse{evalV1Resp: res},
		s.metrics,
		s.contextValues,
		s.headerToContextKeyMappings,
	)
	if err != nil {
		span.RecordError(err)
		span.SetError(fmt.Sprintf("error evaluating flag with key %s", req.Msg.GetFlagKey()))
	}

	return res, err
}

func (s *FlagEvaluationService) ResolveFloat(
	ctx context.Context,
	req *connect.Request[evalV1.ResolveFloatRequest],
) (*connect.Response[evalV1.ResolveFloatResponse], error) {
	ctx, span := s.flagEvalTracer.Start(ctx, "resolveFloat", telemetry.WithSpanKind(telemetry.SpanKindServer))
	defer span.End()

	selector, err := selectorFromHeader(ctx, s.logger, req.Header())
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	ctx = context.WithValue(ctx, store.SelectorContextKey{}, selector)
	ctx = context.WithValue(ctx, evaluator.ProtoVersionKey, "v1") //nolint:staticcheck // exported string key; changing type is breaking

	res := connect.NewResponse(&evalV1.ResolveFloatResponse{})
	err = resolve(
		ctx,
		span,
		s.logger,
		s.eval.ResolveFloatValue,
		req.Header(),
		req.Msg.GetFlagKey(),
		req.Msg.GetContext(),
		&floatResponse{evalV1Resp: res},
		s.metrics,
		s.contextValues,
		s.headerToContextKeyMappings,
	)
	if err != nil {
		span.RecordError(err)
		span.SetError(fmt.Sprintf("error evaluating flag with key %s", req.Msg.GetFlagKey()))
	}

	return res, err
}

func (s *FlagEvaluationService) ResolveObject(
	ctx context.Context,
	req *connect.Request[evalV1.ResolveObjectRequest],
) (*connect.Response[evalV1.ResolveObjectResponse], error) {
	ctx, span := s.flagEvalTracer.Start(ctx, "resolveObject", telemetry.WithSpanKind(telemetry.SpanKindServer))
	defer span.End()

	selector, err := selectorFromHeader(ctx, s.logger, req.Header())
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	ctx = context.WithValue(ctx, store.SelectorContextKey{}, selector)
	ctx = context.WithValue(ctx, evaluator.ProtoVersionKey, "v1") //nolint:staticcheck // exported string key; changing type is breaking

	res := connect.NewResponse(&evalV1.ResolveObjectResponse{})
	err = resolve(
		ctx,
		span,
		s.logger,
		s.eval.ResolveObjectValue,
		req.Header(),
		req.Msg.GetFlagKey(),
		req.Msg.GetContext(),
		&objectResponse{evalV1Resp: res},
		s.metrics,
		s.contextValues,
		s.headerToContextKeyMappings,
	)
	if err != nil {
		span.RecordError(err)
		span.SetError(fmt.Sprintf("error evaluating flag with key %s", req.Msg.GetFlagKey()))
	}

	return res, err
}
