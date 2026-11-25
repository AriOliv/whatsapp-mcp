package oauth

import (
	"context"

	"github.com/AriOliv/whatsapp-mcp/internal/authctx"
)

// WithSub attaches the authenticated subject (paired phone number) to a context.
// The bearer guard sets it per request; tool handlers read it to pick the
// caller's WhatsApp account. Empty (stdio) means "use the default account".
func WithSub(ctx context.Context, sub string) context.Context {
	return authctx.WithAccount(ctx, sub)
}

// SubFromContext returns the authenticated subject, or "" if none.
func SubFromContext(ctx context.Context) string {
	return authctx.Account(ctx)
}
