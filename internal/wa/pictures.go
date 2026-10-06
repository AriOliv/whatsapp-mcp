package wa

// Profile pictures for list rows. Each picture is one WhatsApp IQ, so a list of
// 50 chats can't fetch them inline every time: URLs are cached per account+JID
// (including "no picture"), fetched with bounded concurrency, and a listing waits
// only briefly — whatever isn't ready yet shows up on the next listing.

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

const (
	pictureTTL      = 6 * time.Hour  // pps.whatsapp.net URLs stay valid for a while; refresh well before
	pictureMissTTL  = 12 * time.Hour // hidden / not set
	pictureErrTTL   = 5 * time.Minute
	pictureWorkers  = 6
	pictureFetchMax = 8 * time.Second
	pictureCacheCap = 20000
)

type pictureEntry struct {
	url     string
	expires time.Time
	pending chan struct{} // closed when an in-flight fetch lands
}

type pictureCache struct {
	mu      sync.Mutex
	entries map[string]*pictureEntry // key: account|jid
	sem     chan struct{}
}

func newPictureCache() *pictureCache {
	return &pictureCache{entries: map[string]*pictureEntry{}, sem: make(chan struct{}, pictureWorkers)}
}

// ProfilePictures returns the picture URL of each JID that has one ("" entries
// are omitted). It starts fetches for unknown/stale JIDs in the background and
// waits up to wait for them; JIDs still loading are simply left out.
func (m *Manager) ProfilePictures(ctx context.Context, account string, jids []string, wait time.Duration) map[string]string {
	out := map[string]string{}
	cli, err := m.clientFor(account)
	if err != nil || len(jids) == 0 {
		return out
	}
	if account == "" {
		account = accountKey(cli.Store.ID)
	}
	c := m.pictures
	now := time.Now()
	var waits []chan struct{}
	var waitJIDs []string
	c.mu.Lock()
	for _, j := range jids {
		if j == "" || j == types.StatusBroadcastJID.String() {
			continue
		}
		key := account + "|" + j
		e := c.entries[key]
		if e != nil && e.pending == nil && now.Before(e.expires) {
			if e.url != "" {
				out[j] = e.url
			}
			continue
		}
		if e == nil {
			if len(c.entries) >= pictureCacheCap {
				c.evictLocked(now)
			}
			e = &pictureEntry{}
			c.entries[key] = e
		}
		if e.pending == nil {
			e.pending = make(chan struct{})
			go m.fetchPicture(cli, key, j, e)
		}
		if e.url != "" { // stale but usable while refreshing
			out[j] = e.url
		}
		waits = append(waits, e.pending)
		waitJIDs = append(waitJIDs, j)
	}
	c.mu.Unlock()

	if len(waits) == 0 || wait <= 0 {
		return out
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for i, ch := range waits {
		select {
		case <-ch:
			c.mu.Lock()
			if e := c.entries[account+"|"+waitJIDs[i]]; e != nil && e.url != "" {
				out[waitJIDs[i]] = e.url
			}
			c.mu.Unlock()
		case <-deadline.C:
			return out
		case <-ctx.Done():
			return out
		}
	}
	return out
}

func (m *Manager) fetchPicture(cli *whatsmeow.Client, key, jidStr string, e *pictureEntry) {
	c := m.pictures
	c.sem <- struct{}{}
	defer func() { <-c.sem }()
	url, ttl := "", pictureErrTTL
	if jid, err := types.ParseJID(jidStr); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), pictureFetchMax)
		info, err := cli.GetProfilePictureInfo(ctx, jid, &whatsmeow.GetProfilePictureParams{Preview: true})
		cancel()
		switch {
		case err == nil && info != nil:
			url, ttl = info.URL, pictureTTL
		case err == nil, errors.Is(err, whatsmeow.ErrProfilePictureNotSet), errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized):
			ttl = pictureMissTTL
		}
	} else {
		ttl = pictureMissTTL
	}
	c.mu.Lock()
	if url != "" || ttl != pictureErrTTL || e.url == "" { // keep the stale URL on a transient error
		e.url = url
	}
	e.expires = time.Now().Add(ttl)
	close(e.pending)
	e.pending = nil
	c.mu.Unlock()
}

// evictLocked drops expired entries, or everything idle if none expired.
func (c *pictureCache) evictLocked(now time.Time) {
	for k, e := range c.entries {
		if e.pending == nil && now.After(e.expires) {
			delete(c.entries, k)
		}
	}
	if len(c.entries) >= pictureCacheCap {
		for k, e := range c.entries {
			if e.pending == nil {
				delete(c.entries, k)
			}
		}
	}
}
