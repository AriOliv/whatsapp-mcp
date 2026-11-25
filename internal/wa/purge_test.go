package wa

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

func newPurgeManager(t *testing.T) (*Manager, *appstore.Store) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dsn := "file:" + filepath.Join(t.TempDir(), "wa.db") + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	st, db, err := appstore.Open(dsn, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, init := range []func(context.Context) error{st.Init, st.InitForms, st.InitOutbox} {
		if err := init(ctx); err != nil {
			t.Fatal(err)
		}
	}
	m, err := New(ctx, db, false, st, "", WithStrictAccounts())
	if err != nil {
		t.Fatal(err)
	}
	m.log = waLog.Noop
	return m, st
}

func saveDevice(t *testing.T, m *Manager, phone string, device uint8) *store.Device {
	t.Helper()
	ctx := context.Background()
	dev := m.container.NewDevice()
	jid := types.NewADJID(phone, 0, device)
	dev.ID = &jid
	dev.Account = &waAdv.ADVSignedDeviceIdentity{
		Details:             []byte{1},
		AccountSignature:    make([]byte, 64),
		AccountSignatureKey: make([]byte, 32),
		DeviceSignature:     make([]byte, 64),
	}
	if err := m.container.PutDevice(ctx, dev); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dev.Contacts.PutPushName(ctx, types.NewJID("5599000000000", types.DefaultUserServer), "Someone"); err != nil {
		t.Fatal(err)
	}
	return dev
}

func TestPurgeDeletesOnlyThatAccount(t *testing.T) {
	ctx := context.Background()
	m, st := newPurgeManager(t)
	const gone, kept = "5511111111111", "5522222222222"
	goneDev := saveDevice(t, m, gone, 3)
	saveDevice(t, m, gone, 1)
	keptDev := saveDevice(t, m, kept, 4)
	for _, acct := range []string{gone, kept} {
		if err := st.SaveMessage(ctx, acct, appstore.Message{ID: "m-" + acct, ChatJID: "x@s.whatsapp.net", TS: 1}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := m.Purge(ctx, gone); err != nil {
		t.Fatal(err)
	}

	devices, err := m.container.GetAllDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || accountKey(devices[0].ID) != kept {
		t.Fatalf("only the other account's device must remain, got %d device(s)", len(devices))
	}
	if n := contactRows(t, st, goneDev.ID.String()); n != 0 {
		t.Errorf("whatsmeow contacts of the purged device survived (%d)", n)
	}
	if n := contactRows(t, st, keptDev.ID.String()); n == 0 {
		t.Error("whatsmeow contacts of the other account were deleted")
	}
	if msgs, _ := st.ListMessages(ctx, gone, "x@s.whatsapp.net", 10); len(msgs) != 0 {
		t.Errorf("purged account still has %d message(s)", len(msgs))
	}
	if msgs, _ := st.ListMessages(ctx, kept, "x@s.whatsapp.net", 10); len(msgs) != 1 {
		t.Error("the other account's history was touched")
	}

	m.writeMessage(ingestItem{account: gone, msg: appstore.Message{ID: "late", ChatJID: "x@s.whatsapp.net", TS: 2}})
	if msgs, _ := st.ListMessages(ctx, gone, "x@s.whatsapp.net", 10); len(msgs) != 0 {
		t.Error("a message queued before the purge was written back after it")
	}
}

func contactRows(t *testing.T, st *appstore.Store, ourJID string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM whatsmeow_contacts WHERE our_jid = ?`, ourJID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStrictManagerNeverFallsBackToTheDefaultAccount(t *testing.T) {
	jid := types.NewADJID("5511111111111", 0, 3)
	cli := &whatsmeow.Client{Store: &store.Device{ID: &jid}}
	for _, strict := range []bool{false, true} {
		m := &Manager{clients: map[string]*whatsmeow.Client{"5511111111111": cli}, def: "5511111111111",
			strict: strict, log: waLog.Noop}
		expired, cancel := context.WithTimeout(context.Background(), -1)
		gotLookup := m.lookupClient("")
		_, clientErr := m.clientFor(expired, "")
		cancel()
		gotAcct := m.acct("")
		if strict {
			if gotLookup != nil || clientErr == nil || !strings.Contains(clientErr.Error(), "no WhatsApp account") || gotAcct != "" {
				t.Fatalf("strict manager resolved \"\" to the default: lookup=%v err=%v acct=%q", gotLookup, clientErr, gotAcct)
			}
			continue
		}
		if gotLookup != cli || gotAcct == "" {
			t.Fatalf("non-strict manager must keep the default fallback: lookup=%v acct=%q", gotLookup, gotAcct)
		}
	}
}

func TestHistorySyncDefault(t *testing.T) {
	cases := []struct {
		env        string
		defaultOff bool
		wantOff    bool
	}{
		{"", false, false},
		{"", true, true},
		{"on", true, false},
		{"true", true, false},
		{"off", false, true},
		{"off", true, true},
	}
	for _, c := range cases {
		if got := historySyncOff(c.env, c.defaultOff); got != c.wantOff {
			t.Errorf("historySyncOff(%q, %v) = %v, want %v", c.env, c.defaultOff, got, c.wantOff)
		}
	}
}

func TestMediaURLsGoThroughTheConfiguredFetcher(t *testing.T) {
	refused := errors.New("refused")
	var seen string
	m := &Manager{fetchURL: func(_ context.Context, u string) ([]byte, error) {
		seen = u
		return nil, refused
	}}
	if _, err := m.loadBytes(context.Background(), "http://169.254.169.254/latest/meta-data"); !errors.Is(err, refused) {
		t.Fatalf("URL media must use the injected fetcher, got %v", err)
	}
	if seen == "" {
		t.Fatal("fetcher was not called")
	}
	if b, err := m.loadBytes(context.Background(), "aGk="); err != nil || string(b) != "hi" {
		t.Fatalf("base64 media must not hit the fetcher: %q, %v", b, err)
	}
}

// Every way an account is logged out must take its stored data with it: the
// logout tool (Logout) and WhatsApp unlinking the device from the phone.
func TestLogoutPathsDeleteStoredData(t *testing.T) {
	cases := []struct {
		name       string
		logout     func(t *testing.T, m *Manager, account string)
		keepMarker bool
	}{
		{"logout tool", func(t *testing.T, m *Manager, account string) {
			if err := m.Logout(context.Background(), account); err == nil || !strings.Contains(err.Error(), "could not be reached") {
				t.Fatalf("with no socket the user must be told to unlink on the phone, got %v", err)
			}
		}, false},
		{"unlinked from the phone", func(t *testing.T, m *Manager, account string) {
			jid := types.NewADJID(account, 0, 3)
			cli := &whatsmeow.Client{Store: &store.Device{ID: &jid}}
			m.mu.Lock()
			m.clients[account] = cli
			m.mu.Unlock()
			m.handler(cli)(&events.LoggedOut{})
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			m, st := newPurgeManager(t)
			var hooked []string
			var mu sync.Mutex
			m.purgeHook = func(_ context.Context, account string) error {
				mu.Lock()
				hooked = append(hooked, account)
				mu.Unlock()
				return nil
			}
			const gone, kept = "5511111111111", "5522222222222"
			saveDevice(t, m, gone, 3)
			saveDevice(t, m, kept, 4)
			for _, acct := range []string{gone, kept} {
				if err := st.SaveMessage(ctx, acct, appstore.Message{ID: "m-" + acct, ChatJID: "x@s.whatsapp.net", TS: 1}); err != nil {
					t.Fatal(err)
				}
			}

			if err := st.MarkLoggedOut(ctx, gone, "earlier", time.Now()); err != nil {
				t.Fatal(err)
			}
			c.logout(t, m, gone)

			deadline := time.Now().Add(5 * time.Second)
			for {
				msgs, _ := st.ListMessages(ctx, gone, "x@s.whatsapp.net", 10)
				devices, _ := m.container.GetAllDevices(ctx)
				if len(msgs) == 0 && len(devices) == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("after logout %d message(s) and %d device(s) remain", len(msgs), len(devices))
				}
				time.Sleep(20 * time.Millisecond)
			}
			if msgs, _ := st.ListMessages(ctx, kept, "x@s.whatsapp.net", 10); len(msgs) != 1 {
				t.Fatal("another account's history was touched")
			}
			mu.Lock()
			gotHook := len(hooked) == 1 && hooked[0] == gone
			mu.Unlock()
			if !gotHook {
				t.Fatalf("purge hook calls = %v, want [%s]", hooked, gone)
			}
			marks, _ := st.LoggedOutAccounts(ctx)
			if _, marked := marks[gone]; marked != c.keepMarker {
				t.Fatalf("needs-repair marker present = %v, want %v", marked, c.keepMarker)
			}
		})
	}
}
