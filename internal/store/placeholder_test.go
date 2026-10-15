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

// An account unlinked before logged_out_accounts existed left no device row, so
// a surviving OAuth refresh token is the only evidence it was ever paired.
// Without the backfill the first deploy reports needs_repair: 0 while the
// account is dead, which reads as "all clear".
func TestBackfillMarksTokenWithoutDevice(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if _, err := st.db.ExecContext(ctx, `CREATE TABLE oauth_refresh_tokens (
		token TEXT PRIMARY KEY, client_id TEXT, sub TEXT, resource TEXT, scopes TEXT, expires_at BIGINT)`); err != nil {
		t.Fatalf("create oauth table: %v", err)
	}
	for _, sub := range []string{"5511111111111", "5522222222222", "5533333333333"} {
		if _, err := st.db.ExecContext(ctx,
			`INSERT INTO oauth_refresh_tokens (token, sub) VALUES (?,?)`, "tok-"+sub, sub); err != nil {
			t.Fatalf("seed token: %v", err)
		}
	}

	// Only the first still has a device; the other two were unlinked.
	marked, err := st.BackfillLoggedOut(ctx, []string{"5511111111111"}, time.Now())
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if len(marked) != 2 {
		t.Fatalf("want the 2 device-less accounts marked, got %v", marked)
	}
	if marked[0] != "5522222222222" || marked[1] != "5533333333333" {
		t.Fatalf("unexpected accounts marked: %v", marked)
	}
	if marks, _ := st.LoggedOutAccounts(ctx); len(marks) != 2 {
		t.Fatalf("markers should be persisted, got %+v", marks)
	}

	// Running again must be a no-op: it is an estimate, and re-running it must
	// not move a timestamp that is already recorded.
	again, err := st.BackfillLoggedOut(ctx, []string{"5511111111111"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second run must mark nothing, got %v", again)
	}
}

// A real logout timestamp must win over the startup estimate.
func TestBackfillDoesNotOverwriteRealLogoutTime(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if _, err := st.db.ExecContext(ctx, `CREATE TABLE oauth_refresh_tokens (
		token TEXT PRIMARY KEY, client_id TEXT, sub TEXT, resource TEXT, scopes TEXT, expires_at BIGINT)`); err != nil {
		t.Fatalf("create oauth table: %v", err)
	}
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO oauth_refresh_tokens (token, sub) VALUES (?,?)`, "tok", "5544444444444"); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	real := time.Now().Add(-48 * time.Hour).Truncate(time.Millisecond)
	if err := st.MarkLoggedOut(ctx, "5544444444444", "LoggedOut", real); err != nil {
		t.Fatalf("mark: %v", err)
	}

	if _, err := st.BackfillLoggedOut(ctx, nil, time.Now()); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	marks, _ := st.LoggedOutAccounts(ctx)
	if got := marks["5544444444444"]; !got.Equal(real) {
		t.Fatalf("real logout time must survive the estimate: want %s, got %s", real, got)
	}
}

// stdio mode has no OAuth table at all; the backfill must stay quiet.
func TestBackfillWithoutOAuthTableIsNoOp(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	marked, err := st.BackfillLoggedOut(ctx, nil, time.Now())
	if err != nil {
		t.Fatalf("missing oauth table must not be an error: %v", err)
	}
	if len(marked) != 0 {
		t.Fatalf("nothing to mark, got %v", marked)
	}
}
