package wa

import (
	"context"
	"sync"
	"time"
)

// namesRefreshTimeout is deliberately generous. A refresh is not just a WhatsApp
// round-trip: whatsmeow also writes every contact it learned along the way
// (PutManyRedactedPhones — hundreds of rows for an account in many groups).
// Cutting that short leaves the write half-done, fails it with "transaction:
// begin: context deadline exceeded", and poisons the pooled connection it was
// using ("driver: bad connection"). So a refresh gets room to finish even after
// the caller has stopped waiting for it.
const namesRefreshTimeout = 2 * time.Minute

// fetchNames retrieves the display names for one account.
type fetchNames func(context.Context) (map[string]string, error)

// namesCache holds per-account display names and refreshes them under a single
// flight: concurrent callers share one lookup instead of each starting its own.
//
// The single flight is the point. Every refresh makes whatsmeow write hundreds
// of contact rows, and several of those at once exhaust the connection pool that
// message decryption also needs — which stalls reception for every account,
// because whatsmeow handles nodes serially. Sharing one refresh keeps a burst of
// tool calls costing exactly one write.
type namesCache struct {
	ttl     time.Duration
	slow    func(key string, took time.Duration, err error) // reports a refresh worth noticing
	mu      sync.Mutex
	entries map[string]*namesEntry
}

// slowRefresh is how long a refresh may take before it is worth a log line. The
// fetch is a WhatsApp round-trip plus whatsmeow's own bookkeeping writes, and
// when it drags it is the first thing worth knowing about.
const slowRefresh = 3 * time.Second

type namesEntry struct {
	at    time.Time
	names map[string]string
	done  chan struct{} // non-nil while a refresh is in flight
}

func newNamesCache(ttl time.Duration, slow func(key string, took time.Duration, err error)) *namesCache {
	return &namesCache{ttl: ttl, slow: slow, entries: map[string]*namesEntry{}}
}

// lookup returns the names for an account, refreshing them when stale. It waits
// at most wait for a refresh to land, and only when it has nothing at all to
// show; once any answer is cached, callers get it immediately while the refresh
// continues in the background.
func (c *namesCache) lookup(ctx context.Context, key string, wait time.Duration, fetch fetchNames) map[string]string {
	c.mu.Lock()
	e := c.entries[key]
	if e == nil {
		e = &namesEntry{}
		c.entries[key] = e
	}
	if e.names != nil && time.Since(e.at) < c.ttl {
		names := e.names
		c.mu.Unlock()
		return names
	}
	inFlight := e.done
	if inFlight == nil {
		inFlight = make(chan struct{})
		e.done = inFlight
		go c.refresh(key, inFlight, fetch)
	}
	stale := e.names
	c.mu.Unlock()

	// Never make a caller wait when we already have an answer. A refresh can
	// take minutes — GetJoinedGroups is a round-trip over a socket that may be
	// busy with history sync — and names that are a few minutes old are worth
	// far more to a listing than a fresh answer nobody waited around for.
	if stale != nil {
		return stale
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-inFlight:
	case <-timer.C:
		return stale // caller falls back to a name derived from the JID
	case <-ctx.Done():
		return stale
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[key].names
}

// refresh runs one lookup on its own context and publishes the result. A failed
// fetch keeps whatever names are cached rather than replacing them with nothing.
func (c *namesCache) refresh(key string, done chan struct{}, fetch fetchNames) {
	ctx, cancel := context.WithTimeout(context.Background(), namesRefreshTimeout)
	defer cancel()
	start := time.Now()
	names, err := fetch(ctx)
	if took := time.Since(start); c.slow != nil && (err != nil || took > slowRefresh) {
		c.slow(key, took, err)
	}

	c.mu.Lock()
	e := c.entries[key]
	if err == nil {
		e.at, e.names = time.Now(), names
	}
	e.done = nil
	c.mu.Unlock()
	close(done)
}
