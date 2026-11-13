package throttle

import "context"

type waitContextKey struct{}

// WithWait attaches a wait accumulator to a logical request context.
func WithWait(ctx context.Context, waited *Wait) context.Context {
	return context.WithValue(ctx, waitContextKey{}, waited)
}

// WaitFromContext returns the accumulator, or nil if none was attached.
func WaitFromContext(ctx context.Context) *Wait {
	waited, _ := ctx.Value(waitContextKey{}).(*Wait)
	return waited
}
