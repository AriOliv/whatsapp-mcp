package wa

import (
	"context"
	"strings"
)

// Option adjusts a Manager at construction.
type Option func(*Manager)

// WithStrictAccounts stops an empty account from falling back to the default
// (first paired) account. Multi-tenant deployments need this so a caller with
// no number of their own can never act on someone else's.
func WithStrictAccounts() Option {
	return func(m *Manager) { m.strict = true }
}

// WithHistorySyncDefaultOff turns history sync off unless WA_HISTORY_SYNC
// explicitly enables it.
func WithHistorySyncDefaultOff() Option {
	return func(m *Manager) { m.historyDefaultOff = true }
}

// WithMediaFetcher replaces the HTTP client used for media given by URL.
func WithMediaFetcher(fetch func(ctx context.Context, url string) ([]byte, error)) Option {
	return func(m *Manager) { m.fetchURL = fetch }
}

// WithPurgeHook runs fn for every account whose data is deleted, so state kept
// outside this package (OAuth refresh tokens) goes with it.
func WithPurgeHook(fn func(ctx context.Context, account string) error) Option {
	return func(m *Manager) { m.purgeHook = fn }
}

func historySyncOff(envVal string, defaultOff bool) bool {
	if strings.TrimSpace(envVal) == "" {
		return defaultOff
	}
	return isHistorySyncDisabled(envVal)
}
