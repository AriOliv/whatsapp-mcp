package oauth

import "context"

type subCtxKey struct{}

// WithSub attaches the authenticated subject (paired phone number) to a context.
// The bearer guard sets it per request; tool handlers read it to pick the
// caller's WhatsApp account. Empty (stdio) means "use the default account".
func WithSub(ctx context.Context, sub string) context.Context {
	return context.WithValue(ctx, subCtxKey{}, sub)
}

// SubFromContext returns the authenticated subject, or "" if none.
func SubFromContext(ctx context.Context) string {
	s, _ := ctx.Value(subCtxKey{}).(string)
	return s
}
