package throttle

import "context"

type waitContextKey struct{}
type backgroundContextKey struct{}

// WithWait attaches a wait accumulator to a logical request context.
func WithWait(ctx context.Context, waited *Wait) context.Context {
	return context.WithValue(ctx, waitContextKey{}, waited)
}

// WaitFromContext returns the accumulator, or nil if none was attached.
func WaitFromContext(ctx context.Context) *Wait {
	waited, _ := ctx.Value(waitContextKey{}).(*Wait)
	return waited
}

// WithBackground marks one history-page request for low-priority admission.
func WithBackground(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundContextKey{}, true)
}

// BackgroundFromContext reports whether low-priority admission was requested.
func BackgroundFromContext(ctx context.Context) bool {
	background, _ := ctx.Value(backgroundContextKey{}).(bool)
	return background
}
