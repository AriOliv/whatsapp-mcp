// Package authctx carries the authenticated caller through a request context:
// the WhatsApp account (phone number) a tool call acts on and, in gateway mode,
// the owner email that account belongs to.
package authctx

import "context"

type (
	accountKey struct{}
	ownerKey   struct{}
)

// WithAccount attaches the WhatsApp account (phone digits) a request acts on.
func WithAccount(ctx context.Context, account string) context.Context {
	return context.WithValue(ctx, accountKey{}, account)
}

// Account returns the account set by WithAccount, or "".
func Account(ctx context.Context) string {
	s, _ := ctx.Value(accountKey{}).(string)
	return s
}

// WithOwner attaches the verified owner identity (an email) of a gateway request.
func WithOwner(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, ownerKey{}, owner)
}

// Owner returns the owner set by WithOwner, or "".
func Owner(ctx context.Context) string {
	s, _ := ctx.Value(ownerKey{}).(string)
	return s
}
