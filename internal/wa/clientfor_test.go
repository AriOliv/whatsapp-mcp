package wa

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// An account that was never paired is a user error, not a transient state: it
// must fail immediately instead of burning the reconnect grace period.
func TestClientForUnknownAccountFailsFast(t *testing.T) {
	m := &Manager{clients: map[string]*whatsmeow.Client{}, log: waLog.Noop}
	start := time.Now()
	_, err := m.clientFor(context.Background(), "5511999999999")
	if err == nil {
		t.Fatal("want an error for an unpaired account")
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("should fail immediately, took %s", took)
	}
	if !strings.Contains(err.Error(), "pair one first") {
		t.Fatalf("error should tell the user to pair, got %q", err)
	}
}

// A caller whose context is already past its deadline must not be made to wait
// out the grace period on top of it.
func TestClientForRespectsCallerDeadline(t *testing.T) {
	m := &Manager{clients: map[string]*whatsmeow.Client{}, log: waLog.Noop}
	ctx, cancel := context.WithTimeout(context.Background(), -time.Second)
	defer cancel()
	start := time.Now()
	if _, err := m.clientFor(ctx, "nope"); err == nil {
		t.Fatal("want an error")
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("expired context should return at once, took %s", took)
	}
}

// lookupClient backs the callers that only read the local device store. It must
// never block, so a listing still renders while the socket is down.
func TestLookupClientNeverBlocks(t *testing.T) {
	m := &Manager{clients: map[string]*whatsmeow.Client{}, log: waLog.Noop}
	start := time.Now()
	if cli := m.lookupClient("absent"); cli != nil {
		t.Fatal("want nil for an unknown account")
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Fatalf("lookupClient must not wait, took %s", took)
	}
}
