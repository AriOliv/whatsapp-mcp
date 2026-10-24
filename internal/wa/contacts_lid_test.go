package wa

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

type fakeLIDStore struct {
	store.NoopStore
	lidToPN map[string]types.JID
	pnToLID map[string]types.JID
}

func (f *fakeLIDStore) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	return f.lidToPN[lid.User], nil
}

func (f *fakeLIDStore) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	return f.pnToLID[pn.User], nil
}

type fakeContactStore struct {
	store.NoopStore
	contacts map[types.JID]types.ContactInfo
}

func (f *fakeContactStore) GetContact(_ context.Context, user types.JID) (types.ContactInfo, error) {
	return f.contacts[user], nil
}

func (f *fakeContactStore) GetAllContacts(_ context.Context) (map[types.JID]types.ContactInfo, error) {
	return f.contacts, nil
}

func TestResolveContactInfoAndSenderNamesAcrossLIDAndPN(t *testing.T) {
	ctx := context.Background()
	pnJID := types.NewJID("5511988887777", types.DefaultUserServer)
	lidJID := types.NewJID("148060525084766", types.HiddenUserServer)
	unlistedPN := types.NewJID("5511977776666", types.DefaultUserServer)
	unlistedLID := types.NewJID("999888777666555", types.HiddenUserServer)

	lids := &fakeLIDStore{
		lidToPN: map[string]types.JID{
			lidJID.User:      pnJID,
			unlistedLID.User: unlistedPN,
		},
		pnToLID: map[string]types.JID{
			pnJID.User:      lidJID,
			unlistedPN.User: unlistedLID,
		},
	}
	contacts := &fakeContactStore{
		contacts: map[types.JID]types.ContactInfo{
			pnJID:  {Found: true, FullName: "Carlos Mendes", FirstName: "Carlos"},
			lidJID: {Found: true, PushName: "Carlinhos"},
		},
	}

	acctJID := types.NewJID("5521967582535", types.DefaultUserServer)
	cli := &whatsmeow.Client{
		Store: &store.Device{
			ID:       &acctJID,
			LIDs:     lids,
			Contacts: contacts,
		},
	}
	m := &Manager{
		clients:   map[string]*whatsmeow.Client{"5521967582535": cli},
		loggedOut: map[string]time.Time{},
		log:       waLog.Noop,
	}

	// 1. Companion AD JID (@lid with :18 device) resolves FullName from @s.whatsapp.net.
	adLID := types.JID{User: lidJID.User, Device: 18, Server: types.HiddenUserServer}
	info := resolveContactInfo(ctx, cli, adLID)
	if got := bestContactName(info); got != "Carlos Mendes" {
		t.Fatalf("bestContactName(adLID) = %q, want Carlos Mendes", got)
	}
	if info.PushName != "Carlinhos" {
		t.Fatalf("merged PushName = %q, want Carlinhos", info.PushName)
	}

	// 2. FirstName precedence when FullName is empty.
	if got := bestContactName(types.ContactInfo{FirstName: "Ana", PushName: "Aninha"}); got != "Ana" {
		t.Fatalf("bestContactName(FirstName) = %q, want Ana", got)
	}

	// 3. SenderNames resolves named @lid sender and formats unlisted @lid sender as +<phone>.
	names := m.SenderNames(ctx, "5521967582535", []appstore.Message{
		{ID: "m1", SenderJID: adLID.String()},
		{ID: "m2", SenderJID: unlistedLID.String()},
	})
	if names[adLID.String()] != "Carlos Mendes" {
		t.Fatalf("SenderNames[adLID] = %q, want Carlos Mendes", names[adLID.String()])
	}
	if names[unlistedLID.String()] != "+5511977776666" {
		t.Fatalf("SenderNames[unlistedLID] = %q, want +5511977776666", names[unlistedLID.String()])
	}
}

func TestListMessagesUnifiesLIDAndPNAndIsolatesAccounts(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "test.db") + "?_pragma=foreign_keys(1)"
	st, _, err := appstore.Open(dsn, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}

	pnJID := types.NewJID("5511988887777", types.DefaultUserServer)
	lidJID := types.NewJID("148060525084766", types.HiddenUserServer)
	lids := &fakeLIDStore{
		lidToPN: map[string]types.JID{lidJID.User: pnJID},
		pnToLID: map[string]types.JID{pnJID.User: lidJID},
	}

	acctAJID := types.NewJID("5511111111111", types.DefaultUserServer)
	acctBJID := types.NewJID("5522222222222", types.DefaultUserServer)
	cliA := &whatsmeow.Client{Store: &store.Device{ID: &acctAJID, LIDs: lids, Contacts: &fakeContactStore{}}}
	cliB := &whatsmeow.Client{Store: &store.Device{ID: &acctBJID, LIDs: lids, Contacts: &fakeContactStore{}}}

	m := &Manager{
		store:     st,
		clients:   map[string]*whatsmeow.Client{"5511111111111": cliA, "5522222222222": cliB},
		loggedOut: map[string]time.Time{},
		log:       waLog.Noop,
	}

	// Account A receives an older message on @s.whatsapp.net and a newer one on @lid
	// (with a sender PushName fallback).
	if err := st.SaveMessage(ctx, "5511111111111", appstore.Message{
		ID: "old_pn", ChatJID: pnJID.String(), SenderJID: pnJID.String(), TS: 1000, Body: "mensagem antiga PN",
	}); err != nil {
		t.Fatalf("SaveMessage old_pn: %v", err)
	}
	if err := st.SaveMessage(ctx, "5511111111111", appstore.Message{
		ID: "new_lid", ChatJID: lidJID.String(), SenderJID: lidJID.String(), TS: 2000, Body: "mensagem nova LID", ChatName: "Carlos Push",
	}); err != nil {
		t.Fatalf("SaveMessage new_lid: %v", err)
	}

	// Querying Account A by bare number, @s.whatsapp.net, or @lid returns both messages.
	for _, q := range []string{"5511988887777", pnJID.String(), lidJID.String()} {
		msgs, err := m.ListMessages(ctx, "5511111111111", q, 10)
		if err != nil {
			t.Fatalf("ListMessages(%q): %v", q, err)
		}
		if len(msgs) != 2 || msgs[0].ID != "new_lid" || msgs[1].ID != "old_pn" {
			t.Fatalf("ListMessages(%q) = %+v, want [new_lid, old_pn]", q, msgs)
		}
	}

	// ChatName for Account A resolves the fallback PushName across both JID variants.
	if got := m.ChatName(ctx, "5511111111111", pnJID.String()); got != "Carlos Push" {
		t.Fatalf("ChatName(acctA, pn) = %q, want Carlos Push", got)
	}

	// NEGATIVE ISOLATION TEST: Account B querying Account A's chat JIDs gets nothing.
	for _, q := range []string{"5511988887777", pnJID.String(), lidJID.String()} {
		msgsB, err := m.ListMessages(ctx, "5522222222222", q, 10)
		if err != nil {
			t.Fatalf("ListMessages(acctB, %q): %v", q, err)
		}
		if len(msgsB) != 0 {
			t.Fatalf("cross-account leak: Account B saw Account A messages for %q: %+v", q, msgsB)
		}
	}
	if got := m.ChatName(ctx, "5522222222222", lidJID.String()); got != "" {
		t.Fatalf("cross-account leak: Account B saw Account A ChatName %q", got)
	}
	chatsB, err := m.ListChats(ctx, "5522222222222", 50)
	if err != nil {
		t.Fatalf("ListChats(acctB): %v", err)
	}
	if len(chatsB) != 0 {
		t.Fatalf("cross-account leak: Account B saw Account A chats: %+v", chatsB)
	}
}
