package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// GRPCSourceSettings are the settings a gRPC sync source reads from the `grpc` object of its source. They are all
// opt-in: absent, zero or false means "not set", key by key, and an empty object changes nothing. The object is decoded
// strictly, unlike the rest of the source: an unknown key or a value of the wrong type is an error. The sync provider
// of the source validates the values and reads them into its options.
type GRPCSourceSettings struct {
	// RetryBackoffMs and RetryBackoffMaxMs are the initial and the largest delay of the retry policy and of the
	// reconnect delay, in milliseconds.
	RetryBackoffMs    int64 `json:"retryBackoffMs,omitempty"`
	RetryBackoffMaxMs int64 `json:"retryBackoffMaxMs,omitempty"`
	// StreamDeadlineMs recycles the sync stream after this many milliseconds.
	StreamDeadlineMs int64 `json:"streamDeadlineMs,omitempty"`
	// KeepAliveTime is the interval of HTTP/2 keepalive pings in milliseconds, at least 10000.
	KeepAliveTime int64 `json:"keepAliveTime,omitempty"`
	// FatalStatusCodes are the names of the gRPC status codes that end flagd when they arrive before the source's first
	// payload.
	FatalStatusCodes []string `json:"fatalStatusCodes,omitempty"`
	// SelectorHeader sends the selector as the flagd-selector header too.
	SelectorHeader bool `json:"selectorHeader,omitempty"`
	// ReadyOnFirstPayload makes the source ready when its first payload arrived, not when its stream opened.
	ReadyOnFirstPayload bool `json:"readyOnFirstPayload,omitempty"`
}

// UnmarshalJSON decodes the object strictly.
func (s *GRPCSourceSettings) UnmarshalJSON(data []byte) error {
	type grpcSettings GRPCSourceSettings
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded grpcSettings
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*s = GRPCSourceSettings(decoded)
	return nil
}

// DecodeGRPCSettings decodes the `grpc` object of a source from a decoded configuration file, strictly, as the JSON of
// --sources is decoded. A nil value is no object.
func DecodeGRPCSettings(raw any) (*GRPCSourceSettings, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("grpc settings: %w", err)
	}
	var settings GRPCSourceSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("grpc settings: %w", err)
	}
	return &settings, nil
}

// UnmarshalJSON decodes a source as before, and its `grpc` object strictly, an error naming the source.
func (c *SourceConfig) UnmarshalJSON(data []byte) error {
	type plain SourceConfig
	aux := struct {
		*plain
		GRPC json.RawMessage `json:"grpc"`
	}{plain: (*plain)(c)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	c.GRPC = nil
	if len(aux.GRPC) == 0 || string(aux.GRPC) == "null" {
		return nil
	}
	var settings GRPCSourceSettings
	if err := json.Unmarshal(aux.GRPC, &settings); err != nil {
		return fmt.Errorf("source %q: grpc settings: %w", c.URI, err)
	}
	c.GRPC = &settings
	return nil
}
