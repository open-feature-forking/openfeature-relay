// Package eventsink is the event sink component type: a sink delivers batches of events to an external system,
// and the core buffers, batches and retries for it (UC-020 BR-002). An event sink carries product data that must be
// delivered, such as experiment exposure, analytics or audit; it never replaces the telemetry provider (BR-007).
package eventsink

import (
	"context"
	"errors"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
)

// Sink delivers batches of events to an external system. A sink may implement io.Closer; the pipeline closes it
// after the last delivery.
type Sink interface {
	// Deliver delivers the batch and reports whether it succeeded. The batch is only valid during the call.
	// An error is retried with backoff, unless it is wrapped by Permanent. Deliver returns when ctx ends.
	Deliver(ctx context.Context, batch []events.Event) error
}

type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent marks a delivery failure that repeating cannot fix, such as a refused batch: the pipeline drops the
// batch, counts it and does not retry it. Permanent(nil) is nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

// IsPermanent reports whether err was marked by Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}
