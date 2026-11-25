package gateway

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/AriOliv/whatsapp-mcp/internal/store"
)

const (
	alice  = "alice@example.com"
	bob    = "bob@example.com"
	phoneA = "5511111111111"
	phoneB = "5522222222222"
)

// fakeLinker issues a code at once and then waits for the test to say which
// number scanned it (confirm), or for the flow to be cancelled.
type fakeLinker struct {
	mu      sync.Mutex
	pairs   map[string]int
	confirm map[string]chan string
	results map[string]chan error
	purged  []string
}

func newFakeLinker() *fakeLinker {
	return &fakeLinker{pairs: map[string]int{}, confirm: map[string]chan string{}, results: map[string]chan error{}}
}

func (l *fakeLinker) chans(phone string) (chan string, chan error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.confirm[phone] == nil {
		l.confirm[phone] = make(chan string, 1)
		l.results[phone] = make(chan error, 1)
	}
	return l.confirm[phone], l.results[phone]
}

func (l *fakeLinker) PairPhone(ctx context.Context, phone string, onCode func(string), approve func(string) error) (string, error) {
	l.mu.Lock()
	l.pairs[phone]++
	l.mu.Unlock()
	confirm, results := l.chans(phone)
	onCode("ABCD-1234")
	select {
	case account := <-confirm:
		err := approve(account)
		results <- err
		return account, err
	case <-ctx.Done():
		results <- ctx.Err()
		return "", ctx.Err()
	}
}

func (l *fakeLinker) Purge(_ context.Context, account string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.purged = append(l.purged, account)
	return true, nil
}

func (l *fakeLinker) AccountState(string) string { return "connected" }

// scan simulates the phone that typed the code and waits for the outcome.
func (l *fakeLinker) scan(t *testing.T, requested, scannedBy string) error {
	t.Helper()
	confirm, results := l.chans(requested)
	confirm <- scannedBy
	select {
	case err := <-results:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("pairing did not finish")
		return nil
	}
}

func newBindings(t *testing.T) *store.Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "gw.db") + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	st, _, err := store.Open(dsn, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InitOwners(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestConnectBindsOnlyTheCallerAndOnlyAfterPairing(t *testing.T) {
	ctx := context.Background()
	st, l := newBindings(t), newFakeLinker()
	p := NewPairing(st, l)

	res, err := p.Connect(ctx, alice, "+55 (11) 11111-1111")
	if err != nil {
		t.Fatal(err)
	}
	if res.Code == "" || res.Phone != phoneA || res.Instructions == "" {
		t.Fatalf("connect result = %+v", res)
	}
	if got, _ := st.AccountOf(ctx, alice); got != "" {
		t.Fatalf("bound before WhatsApp confirmed the code: %q", got)
	}
	if err := l.scan(t, phoneA, phoneA); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.AccountOf(ctx, alice); got != phoneA {
		t.Fatalf("alice's binding = %q, want %s", got, phoneA)
	}
	if got, _ := st.AccountOf(ctx, bob); got != "" {
		t.Fatalf("bob was bound to %q by alice's pairing", got)
	}
	if _, err := p.Connect(ctx, bob, phoneA); !errors.Is(err, store.ErrAccountBound) {
		t.Fatalf("bob connecting alice's number = %v, want ErrAccountBound", err)
	}
	if _, err := p.Connect(ctx, alice, phoneB); err == nil {
		t.Fatal("an owner with a number must not start a second pairing")
	}
}

func TestPairingConfirmedByAnotherNumberIsRejected(t *testing.T) {
	ctx := context.Background()
	st, l := newBindings(t), newFakeLinker()
	p := NewPairing(st, l)
	if _, err := p.Connect(ctx, alice, phoneA); err != nil {
		t.Fatal(err)
	}
	if err := l.scan(t, phoneA, phoneB); err == nil {
		t.Fatal("a code confirmed by a different number must be rejected")
	}
	for _, phone := range []string{phoneA, phoneB} {
		if owner, _ := st.OwnerOf(ctx, phone); owner != "" {
			t.Fatalf("%s got bound to %s", phone, owner)
		}
	}
	st2, _ := p.Status(ctx, alice)
	if st2.Pairing == nil || st2.Pairing.State != "failed" {
		t.Fatalf("status must report the failed pairing, got %+v", st2.Pairing)
	}
}

func TestPairingFlowLimits(t *testing.T) {
	ctx := context.Background()
	st, l := newBindings(t), newFakeLinker()
	p := NewPairing(st, l)
	p.max = 2

	first, err := p.Connect(ctx, alice, phoneA)
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Connect(ctx, alice, phoneA)
	if err != nil || again.Code != first.Code {
		t.Fatalf("repeating connect for the pending number must return the same code: %+v, %v", again, err)
	}
	l.mu.Lock()
	started := l.pairs[phoneA]
	l.mu.Unlock()
	if n := started; n != 1 {
		t.Fatalf("a repeat must not start another WhatsApp pairing, started %d", n)
	}
	if _, err := p.Connect(ctx, alice, phoneB); err == nil {
		t.Fatal("one owner must not run two pairings at once")
	}
	if _, err := p.Connect(ctx, bob, phoneA); err == nil {
		t.Fatal("two owners must not pair the same number at once")
	}
	if _, err := p.Connect(ctx, bob, phoneB); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Connect(ctx, "carol@example.com", "5533333333333"); err == nil {
		t.Fatal("the global cap on concurrent pairings was not enforced")
	}
}

func TestAbandonedPairingExpires(t *testing.T) {
	ctx := context.Background()
	st, l := newBindings(t), newFakeLinker()
	p := NewPairing(st, l)
	p.ttl = 50 * time.Millisecond
	clock := time.Now()
	p.now = func() time.Time { return clock }

	if _, err := p.Connect(ctx, alice, phoneA); err != nil {
		t.Fatal(err)
	}
	_, results := l.chans(phoneA)
	select {
	case err := <-results:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("flow ended with %v, want its deadline", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an abandoned pairing was never cancelled")
	}
	if got, _ := st.AccountOf(ctx, alice); got != "" {
		t.Fatal("an expired pairing must not bind")
	}
	if _, err := p.Connect(ctx, alice, phoneB); err == nil {
		t.Fatal("a retry right after a failed pairing must wait out the cooldown")
	}
	clock = clock.Add(pairingCooldown)
	if _, err := p.Connect(ctx, alice, phoneB); err != nil {
		t.Fatalf("after expiry and cooldown a new pairing must start: %v", err)
	}
}

func TestDisconnectPurgesOnlyTheCallersNumber(t *testing.T) {
	ctx := context.Background()
	st, l := newBindings(t), newFakeLinker()
	p := NewPairing(st, l)
	if err := st.Bind(ctx, alice, phoneA); err != nil {
		t.Fatal(err)
	}
	if err := st.Bind(ctx, bob, phoneB); err != nil {
		t.Fatal(err)
	}

	res, err := p.Disconnect(ctx, alice)
	if err != nil || !res.DataDeleted || res.Phone != phoneA {
		t.Fatalf("disconnect = %+v, %v", res, err)
	}
	if len(l.purged) != 1 || l.purged[0] != phoneA {
		t.Fatalf("purged %v, want only %s", l.purged, phoneA)
	}
	if got, _ := st.AccountOf(ctx, alice); got != "" {
		t.Fatal("alice's binding survived disconnect")
	}
	if got, _ := st.AccountOf(ctx, bob); got != phoneB {
		t.Fatal("bob's binding was touched")
	}

	if _, err := p.Disconnect(ctx, "carol@example.com"); err != nil {
		t.Fatal(err)
	}
	if len(l.purged) != 1 {
		t.Fatalf("an owner without a number must purge nothing, purged %v", l.purged)
	}
}
