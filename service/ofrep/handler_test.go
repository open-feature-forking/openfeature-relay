package ofrep

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	mock "github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/mock"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service/ofrep"
	"github.com/open-feature-forking/openfeature-relay/service/transport/middleware"
	compressmw "github.com/open-feature-forking/openfeature-relay/service/transport/middleware/compress"
	"github.com/open-feature-forking/openfeature-relay/telemetry/otel/oteltest"
	"github.com/stretchr/testify/require"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.uber.org/mock/gomock"
)

var flagKey = "key"

var successValue = evaluator.AnyValue{
	Value:    true,
	Variant:  "true",
	Reason:   model.StaticReason,
	FlagKey:  flagKey,
	Metadata: nil,
	Error:    nil,
}

var flagNotFoundValue = evaluator.AnyValue{
	Reason:  model.ErrorReason,
	FlagKey: flagKey,
	Error:   errors.New(model.FlagNotFoundErrorCode),
}

var genericErrorValue = evaluator.AnyValue{
	Reason:  model.ErrorReason,
	FlagKey: flagKey,
	Error:   errors.New(model.GeneralErrorCode),
}

func Test_handler_HandleFlagEvaluation(t *testing.T) {
	log := logger.NewLogger(nil, false)

	tests := []struct {
		name string

		method          string
		path            string
		input           *bytes.Reader
		mockAnyResponse *evaluator.AnyValue

		expectedStatus       int
		expectedResponseType interface{}
	}{
		{
			name:                 "success evaluation",
			method:               http.MethodPost,
			path:                 "/ofrep/v1/evaluate/flags/" + flagKey,
			input:                bytes.NewReader([]byte{}),
			mockAnyResponse:      &successValue,
			expectedStatus:       http.StatusOK,
			expectedResponseType: ofrep.EvaluationSuccess{},
		},
		{
			name:                 "valid context and success",
			method:               http.MethodPost,
			path:                 "/ofrep/v1/evaluate/flags/" + flagKey,
			input:                bytes.NewReader([]byte("{\"context\": {}}")),
			mockAnyResponse:      &successValue,
			expectedStatus:       http.StatusOK,
			expectedResponseType: ofrep.EvaluationSuccess{},
		},
		{
			name:                 "flag not found evaluation",
			method:               http.MethodPost,
			path:                 "/ofrep/v1/evaluate/flags/" + flagKey,
			input:                bytes.NewReader([]byte{}),
			mockAnyResponse:      &flagNotFoundValue,
			expectedStatus:       http.StatusNotFound,
			expectedResponseType: ofrep.EvaluationError{},
		},
		{
			name:                 "general error evaluation",
			method:               http.MethodPost,
			path:                 "/ofrep/v1/evaluate/flags/" + flagKey,
			input:                bytes.NewReader([]byte{}),
			mockAnyResponse:      &genericErrorValue,
			expectedStatus:       http.StatusBadRequest,
			expectedResponseType: ofrep.EvaluationError{},
		},
		{
			name:                 "flag key parsing error - whitespace",
			method:               http.MethodPost,
			path:                 "/ofrep/v1/evaluate/flags/ ",
			mockAnyResponse:      &successValue,
			input:                bytes.NewReader([]byte{}),
			expectedStatus:       http.StatusOK,
			expectedResponseType: ofrep.EvaluationSuccess{},
		},
		{
			name:                 "invalid context payload",
			method:               http.MethodPost,
			path:                 "/ofrep/v1/evaluate/flags/" + flagKey,
			input:                bytes.NewReader([]byte("{some invalid context}")),
			expectedStatus:       http.StatusBadRequest,
			expectedResponseType: ofrep.EvaluationError{},
		},
		{
			name:   "code default - flag without defaultVariant",
			method: http.MethodPost,
			path:   "/ofrep/v1/evaluate/flags/featureNoDefault",
			input:  bytes.NewReader([]byte{}),
			mockAnyResponse: &evaluator.AnyValue{
				Value:    false, // code default (no defaultVariant)
				Variant:  "",
				Reason:   model.FallbackReason,
				FlagKey:  "featureNoDefault",
				Metadata: nil,
				Error:    nil,
			},
			expectedStatus:       http.StatusOK,
			expectedResponseType: ofrep.EvaluationSuccess{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eval := mock.NewMockIEvaluator(gomock.NewController(t))
			if test.mockAnyResponse != nil {
				eval.EXPECT().
					ResolveAsAnyValue(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(*test.mockAnyResponse)
			}

			h := handler{Logger: log, evaluator: eval}

			request, err := http.NewRequest(test.method, test.path, test.input)
			if err != nil {
				t.Fatalf("error setting up request: %v", err)
			}

			recorder := httptest.NewRecorder()

			router := mux.NewRouter()
			router.HandleFunc(singleEvaluation, h.HandleFlagEvaluation)
			router.ServeHTTP(recorder, request)

			if test.expectedStatus != recorder.Code {
				t.Errorf("expected status code %d, but got %d", test.expectedStatus, recorder.Code)
			}

			output := test.expectedResponseType
			err = json.NewDecoder(recorder.Result().Body).Decode(&output)
			if err != nil {
				t.Errorf("error parsing response to expected response: %v", err)
			}
		})
	}
}

func Test_handler_HandleBulkEvaluation(t *testing.T) {
	log := logger.NewLogger(nil, false)

	tests := []struct {
		name string

		method          string
		input           *bytes.Reader
		mockAnyResponse []evaluator.AnyValue
		mockAnyMetadata model.Metadata
		mockAnyError    error

		expectedStatus int
	}{
		{
			name:            "success evaluation",
			method:          http.MethodPost,
			input:           bytes.NewReader([]byte{}),
			mockAnyResponse: []evaluator.AnyValue{successValue},
			expectedStatus:  http.StatusOK,
		},
		{
			name:            "success & evaluation errors",
			method:          http.MethodPost,
			input:           bytes.NewReader([]byte{}),
			mockAnyResponse: []evaluator.AnyValue{successValue, genericErrorValue, flagNotFoundValue},
			expectedStatus:  http.StatusOK,
		},
		{
			name:            "only evaluation errors",
			method:          http.MethodPost,
			input:           bytes.NewReader([]byte{}),
			mockAnyResponse: []evaluator.AnyValue{genericErrorValue, flagNotFoundValue},
			expectedStatus:  http.StatusOK,
		},
		{
			name:            "handles internal errors and yield 500",
			method:          "http.MethodPost",
			input:           bytes.NewReader([]byte{}),
			mockAnyResponse: []evaluator.AnyValue{},
			mockAnyError:    errors.New("some internal error from evaluator"),
			expectedStatus:  http.StatusInternalServerError,
		},
		{
			name:            "valid context payload",
			method:          http.MethodPost,
			input:           bytes.NewReader([]byte("{\"context\": {}}")),
			mockAnyResponse: []evaluator.AnyValue{},
			expectedStatus:  http.StatusOK,
		},
		{
			name:           "invalid context payload",
			method:         http.MethodPost,
			input:          bytes.NewReader([]byte("{some invalid context}")),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:   "bulk evaluation with code defaults",
			method: http.MethodPost,
			input:  bytes.NewReader([]byte{}),
			mockAnyResponse: []evaluator.AnyValue{
				{
					Value:    true,
					Variant:  "on",
					Reason:   model.StaticReason,
					FlagKey:  "featureWithDefault",
					Metadata: nil,
					Error:    nil,
				},
				{
					Value:    false, // code default (no defaultVariant)
					Variant:  "",
					Reason:   model.FallbackReason,
					FlagKey:  "featureNoDefault",
					Metadata: map[string]interface{}{},
					Error:    nil,
				},
			},
			expectedStatus: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eval := mock.NewMockIEvaluator(gomock.NewController(t))
			eval.EXPECT().ResolveAllValues(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(test.mockAnyResponse, test.mockAnyMetadata, test.mockAnyError).MinTimes(0)

			h := handler{Logger: log, evaluator: eval}

			request, err := http.NewRequest(test.method, "/ofrep/v1/evaluate/flags", test.input)
			if err != nil {
				t.Fatalf("error setting up request: %v", err)
			}

			recorder := httptest.NewRecorder()

			router := mux.NewRouter()
			router.HandleFunc(bulkEvaluation, h.HandleBulkEvaluation)
			router.ServeHTTP(recorder, request)

			if test.expectedStatus != recorder.Code {
				t.Errorf("expected status code %d, but got %d", test.expectedStatus, recorder.Code)
			}
		})
	}
}

func TestWriteJSONResponse(t *testing.T) {
	log := logger.NewLogger(nil, false)
	h := handler{Logger: log}

	tests := []struct {
		name           string
		status         int
		payload        interface{}
		expectedStatus int
	}{
		{
			name:           "success",
			status:         http.StatusOK,
			payload:        successValue,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "marshaling error",
			status:         http.StatusOK,
			payload:        func() {}, // make marshaling to fail
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			h.writeJSONToResponse(test.status, test.payload, recorder)

			if test.expectedStatus != recorder.Code {
				t.Errorf("expected status code %d, but got %d", test.expectedStatus, recorder.Code)
			}

			if test.expectedStatus == http.StatusOK && recorder.Header().Get("Content-Type") != "application/json" {
				t.Error("expected http OK to contain header application/json content type, but header is missing")
			}

			if test.expectedStatus != http.StatusOK {
				// rest of the validations are only for status OK
				return
			}

			b, err := io.ReadAll(recorder.Body)
			if err != nil {
				t.Errorf("error deriving body: %v", err)
			}

			var rsp evaluator.AnyValue
			err = json.Unmarshal(b, &rsp)
			if err != nil {
				t.Errorf("error unmarshelling body: %v", err)
			}

			if !reflect.DeepEqual(test.payload, rsp) {
				t.Errorf("incorrect payload in wire")
			}
		})
	}
}
func TestFlagdContextInvalidContextType(t *testing.T) {
	log := logger.NewLogger(nil, false)

	result := flagdContext(
		log,
		"test-request-id",
		ofrep.Request{Context: "not a map"}, // invalid: string instead of map
		map[string]any{"staticKey": "staticValue"},
		http.Header{},
		map[string]string{},
	)

	if val, exists := result["staticKey"]; !exists || val != "staticValue" {
		t.Errorf("expected static context to be included even with invalid request context")
	}
}

func TestFlagdContextDelegatesContextMerging(t *testing.T) {
	log := logger.NewLogger(nil, false)

	h := http.Header{}
	h.Set("X-User-Tier", "premium")

	result := flagdContext(
		log,
		"test-request-id",
		ofrep.Request{Context: map[string]any{"requestKey": "requestValue"}},
		map[string]any{"staticKey": "staticValue"},
		h,
		map[string]string{"X-User-Tier": "userTier"},
	)

	expected := map[string]any{
		"requestKey": "requestValue",
		"staticKey":  "staticValue",
		"userTier":   "premium",
	}

	for k, v := range expected {
		if result[k] != v {
			t.Errorf("expected key '%s' to have value '%s', but got '%v'", k, v, result[k])
		}
	}
}

func TestInvalidSelector_OFREPHandler(t *testing.T) {
	const invalidSelector = "invalidKey=val"
	log := logger.NewLogger(nil, false)

	t.Run("HandleFlagEvaluation", func(t *testing.T) {
		eval := mock.NewMockIEvaluator(gomock.NewController(t))
		h := handler{Logger: log, evaluator: eval}

		req, _ := http.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags/"+flagKey, bytes.NewReader([]byte{}))
		req.Header.Set("Flagd-Selector", invalidSelector)

		recorder := httptest.NewRecorder()
		router := mux.NewRouter()
		router.HandleFunc(singleEvaluation, h.HandleFlagEvaluation)
		router.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusBadRequest, recorder.Code)
	})

	t.Run("HandleBulkEvaluation", func(t *testing.T) {
		eval := mock.NewMockIEvaluator(gomock.NewController(t))
		h := handler{Logger: log, evaluator: eval}

		req, _ := http.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", bytes.NewReader([]byte{}))
		req.Header.Set("Flagd-Selector", invalidSelector)

		recorder := httptest.NewRecorder()
		router := mux.NewRouter()
		router.HandleFunc(bulkEvaluation, h.HandleBulkEvaluation)
		router.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusBadRequest, recorder.Code)
	})
}

func TestHandlerRecordsSingleEvaluationMetrics(t *testing.T) {
	tests := []struct {
		name       string
		evaluation evaluator.AnyValue
		expected   telemetrytest.Evaluation
	}{
		{
			name: "successful evaluation",
			evaluation: evaluator.AnyValue{
				Value:    successValue.Value,
				Variant:  successValue.Variant,
				Reason:   successValue.Reason,
				FlagKey:  successValue.FlagKey,
				Metadata: model.Metadata{"flagSetId": "payments"},
			},
			expected: telemetrytest.Evaluation{
				Reason:    successValue.Reason,
				Variant:   successValue.Variant,
				FlagKey:   successValue.FlagKey,
				FlagSetID: "payments",
			},
		},
		{
			name: "failed evaluation",
			evaluation: evaluator.AnyValue{
				Reason:   genericErrorValue.Reason,
				FlagKey:  genericErrorValue.FlagKey,
				Metadata: model.Metadata{"flagSetId": "selected-set"},
				Error:    genericErrorValue.Error,
			},
			expected: telemetrytest.Evaluation{
				ErrorText: genericErrorValue.Error.Error(),
				Reason:    genericErrorValue.Reason,
				FlagSetID: "selected-set",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tel := telemetrytest.New()
			eval := mock.NewMockIEvaluator(gomock.NewController(t))
			eval.EXPECT().
				ResolveAsAnyValue(gomock.Any(), gomock.Any(), flagKey, gomock.Any()).
				Return(test.evaluation)
			handler := NewOfrepHandler(logger.NewLogger(nil, false), eval, nil, nil, tel, "flagd", SSEConfig{}, newCompression(t))

			request := httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags/"+flagKey, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			require.Equal(t, []telemetrytest.Evaluation{test.expected}, telemetrytest.Evaluations(t, tel.Metrics))
		})
	}
}

func TestHandlerRecordsEachBulkEvaluationMetric(t *testing.T) {
	tel := telemetrytest.New()
	evaluations := []evaluator.AnyValue{
		{
			Value:    successValue.Value,
			Variant:  successValue.Variant,
			Reason:   successValue.Reason,
			FlagKey:  "flag-a",
			Metadata: model.Metadata{"flagSetId": "set-a"},
		},
		{
			Reason:   genericErrorValue.Reason,
			FlagKey:  "flag-b",
			Metadata: model.Metadata{"flagSetId": "set-b"},
			Error:    genericErrorValue.Error,
		},
		{
			Reason:   flagNotFoundValue.Reason,
			FlagKey:  "flag-c",
			Metadata: model.Metadata{"flagSetId": 42},
			Error:    flagNotFoundValue.Error,
		},
	}
	eval := mock.NewMockIEvaluator(gomock.NewController(t))
	eval.EXPECT().ResolveAllValues(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(evaluations, model.Metadata{}, nil)
	handler := NewOfrepHandler(logger.NewLogger(nil, false), eval, nil, nil, tel, "flagd", SSEConfig{}, newCompression(t))

	request := httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	require.ElementsMatch(t, []telemetrytest.Evaluation{
		{
			Reason:    successValue.Reason,
			Variant:   successValue.Variant,
			FlagKey:   "flag-a",
			FlagSetID: "set-a",
		},
		{
			ErrorText: genericErrorValue.Error.Error(),
			Reason:    genericErrorValue.Reason,
			FlagSetID: "set-b",
		},
		{
			ErrorText: flagNotFoundValue.Error.Error(),
			Reason:    flagNotFoundValue.Reason,
		},
	}, telemetrytest.Evaluations(t, tel.Metrics))
}

// newCompression builds the gzip middleware the production handler always carries. These tests do
// not send Accept-Encoding, so it passes their responses through untouched.
func newCompression(t *testing.T) middleware.IMiddleware {
	t.Helper()

	compression, err := compressmw.New()
	require.NoError(t, err)

	return compression
}

func TestOfrepHTTPMetrics(t *testing.T) {
	evalPath := "/ofrep/v1/evaluate/flags/" + flagKey
	tests := []struct {
		name          string
		method        string
		path          string
		body          string
		response      *evaluator.AnyValue
		expectedCode  string
		expectedRoute string
	}{
		{name: "success", path: evalPath, body: `{"context":{}}`, response: &successValue, expectedCode: "200", expectedRoute: singleEvaluation},
		{name: "flag not found", path: evalPath, body: `{"context":{}}`, response: &flagNotFoundValue, expectedCode: "404", expectedRoute: singleEvaluation},
		{name: "invalid context", path: evalPath, body: `{some invalid context}`, expectedCode: "400", expectedRoute: singleEvaluation},
		{name: "unknown path", path: "/ofrep/v1/unknown", expectedCode: "404", expectedRoute: "/"},
		{name: "method not allowed", method: http.MethodGet, path: evalPath, expectedCode: "405", expectedRoute: "/"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eval := mock.NewMockIEvaluator(gomock.NewController(t))
			if test.response != nil {
				eval.EXPECT().ResolveAsAnyValue(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(*test.response)
			}

			tel := telemetrytest.New()
			h := NewOfrepHandler(logger.NewLogger(nil, false), eval, nil, nil, tel, "flagd", SSEConfig{}, newCompression(t))
			method := test.method
			if method == "" {
				method = http.MethodPost
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(method, test.path, strings.NewReader(test.body)))
			require.Equal(t, test.expectedCode, strconv.Itoa(response.Code), "response status code")

			scopes, points := telemetrytest.RequestDurationPoints(t, tel.Metrics)
			require.Equal(t, map[string]bool{"flagd": true}, scopes, "request should be recorded once, by flagd only")
			require.Len(t, points, 1)
			require.Equal(t, uint64(1), points[0].Count, "request should be recorded once")
			code, _ := points[0].Attributes.Value(semconv.HTTPResponseStatusCodeKey)
			require.Equal(t, test.expectedCode, code.AsString(), "recorded status code")
			route, _ := points[0].Attributes.Value(semconv.HTTPRouteKey)
			require.Equal(t, test.expectedRoute, route.AsString(), "recorded route")
		})
	}
}
