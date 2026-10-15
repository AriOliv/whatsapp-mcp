package store

import (
	"context"
	"testing"
	"time"
)

// A message that failed to decrypt is stored as a placeholder so it is visible
// at all. The decrypted version may arrive later (the sender honours the retry
// receipt), and it must win.
func TestDecryptedMessageReplacesPlaceholder(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	if err := st.SavePlaceholder(ctx, "acct", Message{
		ID: "m1", ChatJID: "g@g.us", SenderJID: "s@s.whatsapp.net", TS: 10,
		Body: "[placeholder]",
	}); err != nil {
		t.Fatalf("save placeholder: %v", err)
	}
	if err := st.SaveMessage(ctx, "acct", Message{
		ID: "m1", ChatJID: "g@g.us", SenderJID: "s@s.whatsapp.net", TS: 10,
		Body: "the real text",
	}); err != nil {
		t.Fatalf("save decrypted: %v", err)
	}

	msgs, err := st.ListMessages(ctx, "acct", "g@g.us", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message (same ID upserts), got %d", len(msgs))
	}
	if msgs[0].Body != "the real text" {
		t.Fatalf("decrypted text should win, got %q", msgs[0].Body)
	}
}

// The resend can also land BEFORE the undecryptable event is drained from the
// ingest queue. A placeholder must never overwrite text we already have —
// that would turn a delivered message back into a blank one.
func TestPlaceholderDoesNotClobberDecryptedMessage(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	if err := st.SaveMessage(ctx, "acct", Message{
		ID: "m1", ChatJID: "g@g.us", TS: 10, Body: "the real text",
	}); err != nil {
		t.Fatalf("save decrypted: %v", err)
	}
	if err := st.SavePlaceholder(ctx, "acct", Message{
		ID: "m1", ChatJID: "g@g.us", TS: 10, Body: "[placeholder]",
	}); err != nil {
		t.Fatalf("save placeholder: %v", err)
	}

	msgs, err := st.ListMessages(ctx, "acct", "g@g.us", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Body != "the real text" {
		t.Fatalf("placeholder must not overwrite real text, got %+v", msgs)
	}
}

// Without this the chat does not surface in a chat list at all: an undecryptable
// message would leave the conversation looking idle.
func TestPlaceholderBumpsChatActivity(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	if err := st.SavePlaceholder(ctx, "acct", Message{
		ID: "m1", ChatJID: "g@g.us", TS: 99, Body: "[placeholder]",
	}); err != nil {
		t.Fatalf("save placeholder: %v", err)
	}
	chats, err := st.ListChats(ctx, "acct", 10)
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 1 || chats[0].JID != "g@g.us" {
		t.Fatalf("chat should appear in listing, got %+v", chats)
	}
	if chats[0].LastTS != 99 {
		t.Fatalf("last activity should be bumped, got %d", chats[0].LastTS)
	}
}

// whatsmeow deletes its own device row when WhatsApp unlinks an account, so our
// marker is the only thing left that knows a human has to re-pair. It has to
// outlive the process — with strategy:Recreate every deploy is a restart.
func TestLoggedOutMarkerSurvivesAndClears(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	when := time.Now().Truncate(time.Millisecond)

	if err := st.MarkLoggedOut(ctx, "5511998362963", "LoggedOut", when); err != nil {
		t.Fatalf("mark: %v", err)
	}
	marks, err := st.LoggedOutAccounts(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got, ok := marks["5511998362963"]; !ok {
		t.Fatalf("marker must persist, got %+v", marks)
	} else if !got.Equal(when) {
		t.Fatalf("timestamp should round-trip: want %s, got %s", when, got)
	}

	// Re-logging the same account must update it, not duplicate it.
	if err := st.MarkLoggedOut(ctx, "5511998362963", "LoggedOut", when.Add(time.Hour)); err != nil {
		t.Fatalf("re-mark: %v", err)
	}
	if marks, _ = st.LoggedOutAccounts(ctx); len(marks) != 1 {
		t.Fatalf("want 1 row after re-mark, got %d", len(marks))
	}

	if err := st.ClearLoggedOut(ctx, "5511998362963"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if marks, _ = st.LoggedOutAccounts(ctx); len(marks) != 0 {
		t.Fatalf("re-pairing must clear the marker, got %+v", marks)
	}
}
