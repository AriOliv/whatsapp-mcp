package store

import (
	"context"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "test.db") +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	st, _, err := Open(dsn, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	return st
}

// Storing the names a listing resolved is what keeps the next listing off the
// network entirely.
func TestSavedChatNamesSurviveIntoLaterListings(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.SaveMessage(ctx, "acct", Message{
		ID: "m1", ChatJID: "a@g.us", TS: 1, Body: "hi",
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}

	if err := st.SaveChatNames(ctx, "acct", map[string]string{
		"a@g.us":     "Group A",
		"ghost@g.us": "Never Seen", // no messages: must not appear
		"blank@g.us": "",           // empty names are not worth storing
	}); err != nil {
		t.Fatalf("save names: %v", err)
	}

	chats, err := st.ListChats(ctx, "acct", 10)
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 1 {
		t.Fatalf("got %d chats, want only the one with messages", len(chats))
	}
	if chats[0].Name != "Group A" {
		t.Fatalf("chat name is %q, want %q", chats[0].Name, "Group A")
	}

	name, err := st.ChatName(ctx, "acct", "a@g.us")
	if err != nil || name != "Group A" {
		t.Fatalf("ChatName returned (%q, %v), want (\"Group A\", nil)", name, err)
	}
}

func TestChatNameIsEmptyForAnUnknownChat(t *testing.T) {
	name, err := newTestStore(t).ChatName(context.Background(), "acct", "nobody@g.us")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "" {
		t.Fatalf("got %q, want an empty name", name)
	}
}
