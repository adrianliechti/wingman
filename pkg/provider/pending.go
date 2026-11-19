package provider

import "context"

type pendingKey struct{}

// OnPending registers fn to run when a model has started to answer while its
// chunks are still withheld, as a router does for as long as it may fail over
// to another model. A streaming transport can then open its response and keep
// the connection alive during a long silent reasoning phase. fn runs on the
// goroutine that iterates the completion and may run more than once.
func OnPending(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, pendingKey{}, fn)
}

// NotifyPending runs the function registered with OnPending, if any.
func NotifyPending(ctx context.Context) {
	if fn, ok := ctx.Value(pendingKey{}).(func()); ok && fn != nil {
		fn()
	}
}
