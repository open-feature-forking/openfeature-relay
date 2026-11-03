package evaluator

import (
	"context"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// Type is the type a caller asks a single flag to resolve to.
type Type int

const (
	// TypeAny asks for the flag's value as it is.
	TypeAny Type = iota
	TypeBool
	TypeString
	TypeInt
	TypeFloat
	TypeObject
)

// Request is one evaluation: a single flag, an explicit list of flags, or every flag the selector names.
type Request struct {
	Selector store.Selector
	// FlagKey names one flag when FlagKeys is nil and Bulk is false. It is a field of its own so that a
	// single-flag request allocates no key slice.
	FlagKey string
	// FlagKeys names flags explicitly; the router uses it when it splits a bulk request.
	FlagKeys []string
	// Bulk marks a request for every flag the selector names, and is kept when the router splits the keys.
	// Evaluators apply their bulk rules when it is set.
	Bulk    bool
	Context map[string]any
	// TypeHint is the type a single-flag caller asks for. The caller checks the result against it; an
	// evaluator may use it to choose between values.
	TypeHint Type
	// Attributes describe the caller, for example its audience. Middleware fills them (see WithAttributes).
	Attributes map[string]any
}

// Result is the resolution of one flag.
type Result struct {
	FlagKey string
	// Format is the configuration format whose evaluator resolved the flag. The router stamps it.
	Format string
	// Value is meaningful when HasValue is set. In bulk results an evaluator may set it without HasValue to
	// keep the typed zero value of a flag it could not resolve.
	Value    any
	HasValue bool
	Variant  string
	// Reason is an OpenFeature reason, or an uppercase vendor reason without parameters. It is never FALLBACK.
	Reason string
	// ErrorCode is one of OpenFeature's error codes; it is empty when the evaluation succeeded.
	ErrorCode    string
	ErrorMessage string
	Metadata     model.Metadata
}

// Evaluator resolves flags. One method serves a single flag and a bulk request alike: the results are
// appended to dst, so that a caller can reuse its buffer, and the flag-set metadata of the selector is returned
// beside them. An error is for a failure of the whole request; a failure of one flag is its Result's ErrorCode.
type Evaluator interface {
	Evaluate(ctx context.Context, req Request, dst []Result) ([]Result, model.Metadata, error)
}
