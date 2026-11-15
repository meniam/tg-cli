package rpc

import "context"

type connectionWaitKey struct{}

func WithConnectionWait(ctx context.Context) context.Context {
	return context.WithValue(ctx, connectionWaitKey{}, true)
}
func NeedsConnectionWait(ctx context.Context) bool {
	v, _ := ctx.Value(connectionWaitKey{}).(bool)
	return v
}
