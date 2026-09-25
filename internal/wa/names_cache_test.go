package wa

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The reason this cache exists: a burst of tool calls must cost one lookup, not
// one per call. Each lookup makes whatsmeow write hundreds of contact rows, and
// several at once starve the pool that message decryption needs.
func TestConcurrentLookupsShareOneFetch(t *testing.T) {
	var fetches atomic.Int32
	release := make(chan struct{})
	c := newNamesCache(time.Minute)

	fetch := func(context.Context) (map[string]string, error) {
		fetches.Add(1)
		<-release // hold every caller inside the flight
		return map[string]string{"a@g.us": "Group A"}, nil
	}

	var wg sync.WaitGroup
	got := make([]map[string]string, 20)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = c.lookup(context.Background(), "acct", time.Second, fetch)
		}()
	}
	time.Sleep(50 * time.Millisecond) // let them all arrive
	close(release)
	wg.Wait()

	if n := fetches.Load(); n != 1 {
		t.Fatalf("ran %d fetches for 20 concurrent lookups, want 1", n)
	}
	for i, names := range got {
		if names["a@g.us"] != "Group A" {
			t.Fatalf("lookup %d got %v, want the fetched name", i, names)
		}
	}
}

// A caller that runs out of patience must not cancel the refresh: whatsmeow is
// still writing the contacts it learned, and killing that mid-transaction
// poisons the connection it was using.
func TestSlowFetchIsNotCancelledWhenTheCallerGivesUp(t *testing.T) {
	c := newNamesCache(time.Minute)
	started := make(chan struct{})
	finished := make(chan struct{})

	fetch := func(ctx context.Context) (map[string]string, error) {
		close(started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err() // would mean the caller cancelled us
		case <-time.After(150 * time.Millisecond):
		}
		close(finished)
		return map[string]string{"a@g.us": "Group A"}, nil
	}

	callerCtx, cancel := context.WithCancel(context.Background())
	if names := c.lookup(callerCtx, "acct", 20*time.Millisecond, fetch); names != nil {
		t.Fatalf("expected no names while the fetch is still running, got %v", names)
	}
	<-started
	cancel() // the caller is gone

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("the refresh was cancelled with its caller")
	}

	names := c.lookup(context.Background(), "acct", time.Second, fetch)
	if names["a@g.us"] != "Group A" {
		t.Fatalf("refresh did not publish its result, got %v", names)
	}
}

func TestFreshEntriesSkipTheFetch(t *testing.T) {
	var fetches atomic.Int32
	c := newNamesCache(time.Minute)
	fetch := func(context.Context) (map[string]string, error) {
		fetches.Add(1)
		return map[string]string{"a@g.us": "Group A"}, nil
	}
	for range 5 {
		c.lookup(context.Background(), "acct", time.Second, fetch)
	}
	if n := fetches.Load(); n != 1 {
		t.Fatalf("ran %d fetches, want 1 (the rest served from cache)", n)
	}
}

// Stale names beat no names: a listing that shows the previous subjects is far
// better than one that shows raw identifiers.
func TestFailedRefreshKeepsPreviousNames(t *testing.T) {
	c := newNamesCache(time.Nanosecond) // everything is immediately stale
	ok := func(context.Context) (map[string]string, error) {
		return map[string]string{"a@g.us": "Group A"}, nil
	}
	c.lookup(context.Background(), "acct", time.Second, ok)

	boom := func(context.Context) (map[string]string, error) {
		return nil, errors.New("offline")
	}
	if names := c.lookup(context.Background(), "acct", time.Second, boom); names["a@g.us"] != "Group A" {
		t.Fatalf("a failed refresh discarded the cached names, got %v", names)
	}
}
