package wa

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
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

// An account WhatsApp unlinked cannot recover on its own: waiting out the
// reconnect grace would stall every call for 20s on something only a human can
// fix, and "pair one first" would be misleading for an account the user thinks
// is live.
func TestClientForLoggedOutAccountFailsFastAndSaysSo(t *testing.T) {
	m := &Manager{
		clients:   map[string]*whatsmeow.Client{},
		loggedOut: map[string]time.Time{"5511998362963": time.Now()},
		log:       waLog.Noop,
	}
	start := time.Now()
	_, err := m.clientFor(context.Background(), "5511998362963")
	if err == nil {
		t.Fatal("want an error for a logged-out account")
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("must not wait out the reconnect grace, took %s", took)
	}
	if !strings.Contains(err.Error(), "re-pair") {
		t.Fatalf("error must say the account needs re-pairing, got %q", err)
	}
}

// A logged-out account keeps no client, so it must still be reported — else it
// disappears from /healthz and looks like it was never paired.
func TestHealthReportsLoggedOutAccountAsNeedsRepair(t *testing.T) {
	m := &Manager{
		clients:            map[string]*whatsmeow.Client{},
		perAccountConnects: map[string]int{},
		loggedOut:          map[string]time.Time{"5511998362963": time.Now()},
		log:                waLog.Noop,
	}
	accounts, _, _ := m.Health()
	if len(accounts) != 1 {
		t.Fatalf("logged-out account must still be listed, got %+v", accounts)
	}
	if accounts[0].State != AccountNeedsRepair {
		t.Fatalf("want state %q, got %q", AccountNeedsRepair, accounts[0].State)
	}
	if accounts[0].LoggedOutAt == "" {
		t.Fatal("want the logout timestamp so a dashboard can show how long it has been down")
	}
}

func TestStaleCompanionLogoutDoesNotEvictActiveClient(t *testing.T) {
	activeJID := types.NewADJID("5521967582535", 0, 37)
	staleJID := types.NewADJID("5521967582535", 0, 35)
	activeCli := &whatsmeow.Client{Store: &store.Device{ID: &activeJID}}
	staleCli := &whatsmeow.Client{Store: &store.Device{ID: &staleJID}}

	m := &Manager{
		clients:            map[string]*whatsmeow.Client{"5521967582535": activeCli},
		perAccountConnects: map[string]int{},
		loggedOut:          map[string]time.Time{},
		log:                waLog.Noop,
	}

	// Stale companion device fires LoggedOut -> must NOT remove activeCli or mark loggedOut.
	m.handler(staleCli)(&events.LoggedOut{Reason: events.ConnectFailureLoggedOut})

	if got := m.clients["5521967582535"]; got != activeCli {
		t.Fatalf("active client was evicted by stale companion logout: got %v, want %v", got, activeCli)
	}
	if _, marked := m.loggedOut["5521967582535"]; marked {
		t.Fatal("account must not be marked loggedOut when a different client is active")
	}

	// Active client itself fires LoggedOut -> now the account transitions to loggedOut.
	m.handler(activeCli)(&events.LoggedOut{Reason: events.ConnectFailureLoggedOut})
	if got := m.clients["5521967582535"]; got != nil {
		t.Fatalf("expected active client to be removed on its own logout, got %v", got)
	}
	if _, marked := m.loggedOut["5521967582535"]; !marked {
		t.Fatal("expected account to be marked loggedOut when active client logs out")
	}
}

func TestRegisterStaleLastAndLoggedOutKeepsActiveClient(t *testing.T) {
	activeJID := types.NewADJID("5521967582535", 0, 37)
	staleJID := types.NewADJID("5521967582535", 0, 35)
	activeDev := &store.Device{ID: &activeJID, Log: waLog.Noop}
	staleDev := &store.Device{ID: &staleJID, Log: waLog.Noop}

	// 1. dedupeDevices in both orders keeps only :37.
	for _, input := range [][]*store.Device{{activeDev, staleDev}, {staleDev, activeDev}} {
		keep, stale := dedupeDevices(input)
		if len(keep) != 1 || keep[0] != activeDev {
			t.Fatalf("dedupeDevices keep=%v, want [activeDev]", keep)
		}
		if len(stale) != 1 || stale[0] != staleDev {
			t.Fatalf("dedupeDevices stale=%v, want [staleDev]", stale)
		}
	}

	// 2. Even if register() is called with active first and stale last,
	// register() keeps the newer device (:37) and a subsequent stale LoggedOut
	// leaves active in m.clients with no loggedOut marker.
	m := &Manager{
		clients:            map[string]*whatsmeow.Client{},
		perAccountConnects: map[string]int{},
		loggedOut:          map[string]time.Time{},
		log:                waLog.Noop,
	}
	activeCli := m.newClient(activeDev)
	staleCli := m.newClient(staleDev)

	m.register(activeCli)
	m.register(staleCli) // stale registered LAST

	if got := m.clients["5521967582535"]; got != activeCli {
		t.Fatalf("register(stale) overwrote newer active client: got %v, want %v", got, activeCli)
	}

	m.handler(staleCli)(&events.LoggedOut{Reason: events.ConnectFailureLoggedOut})

	if got := m.clients["5521967582535"]; got != activeCli {
		t.Fatalf("stale LoggedOut evicted active client: got %v, want %v", got, activeCli)
	}
	if _, marked := m.loggedOut["5521967582535"]; marked {
		t.Fatal("account must not be marked loggedOut after stale companion logout")
	}
}

func TestChatNameUnknownNumberReturnsEmpty(t *testing.T) {
	m := &Manager{
		clients:   map[string]*whatsmeow.Client{},
		loggedOut: map[string]time.Time{},
		log:       waLog.Noop,
	}
	if got := m.ChatName(context.Background(), "5521967582535", "5511999990000"); got != "" {
		t.Fatalf("ChatName for unknown number = %q, want empty string", got)
	}
	if got := m.ChatName(context.Background(), "5521967582535", ""); got != "" {
		t.Fatalf("ChatName for empty number = %q, want empty string", got)
	}
}
