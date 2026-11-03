package evaluator

import "context"

type attributesKey struct{}

// WithAttributes returns a context whose requests carry the attributes.
func WithAttributes(ctx context.Context, attributes map[string]any) context.Context {
	return context.WithValue(ctx, attributesKey{}, attributes)
}

// AttributesFromContext returns the attributes WithAttributes stored, or nil.
func AttributesFromContext(ctx context.Context) map[string]any {
	attributes, _ := ctx.Value(attributesKey{}).(map[string]any)
	return attributes
}
