package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newOwnerStore(t *testing.T) *Store {
	t.Helper()
	st := newTestStore(t)
	if err := st.InitOwners(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestBindEnforcesOneNumberPerOwnerAndOneOwnerPerNumber(t *testing.T) {
	ctx := context.Background()
	st := newOwnerStore(t)
	if err := st.Bind(ctx, "a@example.com", "5511111111111"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		owner   string
		account string
		want    error
	}{
		{"same pair again is idempotent", "a@example.com", "5511111111111", nil},
		{"owner cannot take a second number", "a@example.com", "5522222222222", ErrOwnerBound},
		{"number cannot move to another owner", "b@example.com", "5511111111111", ErrAccountBound},
		{"another owner gets a free number", "b@example.com", "5522222222222", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := st.Bind(ctx, c.owner, c.account); !errors.Is(err, c.want) {
				t.Fatalf("Bind(%s, %s) = %v, want %v", c.owner, c.account, err, c.want)
			}
		})
	}
	if got, _ := st.AccountOf(ctx, "a@example.com"); got != "5511111111111" {
		t.Fatalf("a's account = %q", got)
	}
	if got, _ := st.OwnerOf(ctx, "5522222222222"); got != "b@example.com" {
		t.Fatalf("owner of b's number = %q", got)
	}
	if err := st.Unbind(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.AccountOf(ctx, "a@example.com"); got != "" {
		t.Fatalf("after unbind a still has %q", got)
	}
	if err := st.Bind(ctx, "c@example.com", "5511111111111"); err != nil {
		t.Fatalf("a released number must be bindable again: %v", err)
	}
}

func TestDeleteAccountDataRemovesOnlyThatAccount(t *testing.T) {
	ctx := context.Background()
	st := newOwnerStore(t)
	if err := st.InitForms(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitOutbox(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `CREATE TABLE whatsmeow_privacy_tokens (
		our_jid TEXT, their_jid TEXT, token BLOB NOT NULL, timestamp BIGINT NOT NULL, PRIMARY KEY (our_jid, their_jid))`); err != nil {
		t.Fatal(err)
	}
	const gone, kept = "5511111111111", "5522222222222"
	now := time.Now()
	for _, acct := range []string{gone, kept} {
		if err := st.SaveMessage(ctx, acct, Message{ID: "m-" + acct, ChatJID: "x@s.whatsapp.net", TS: 1, Body: "hi"}); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateForm(ctx, Form{ID: "f-" + acct, TokenHash: "h-" + acct, Account: acct, To: "x",
			Spec: []byte(`{}`), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if err := st.EnqueueOutbox(ctx, acct, "m-"+acct, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		if err := st.MarkLoggedOut(ctx, acct, "test", now); err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.ExecContext(ctx, `INSERT INTO whatsmeow_privacy_tokens VALUES (?, 'x', x'00', 1)`,
			acct+":7@s.whatsapp.net"); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.DeleteAccountData(ctx, gone); err != nil {
		t.Fatal(err)
	}

	tables := []string{"messages", "chats", "forms", "webhook_outbox", "logged_out_accounts"}
	for _, tbl := range tables {
		if n := countWhere(t, st, tbl, "account_jid", gone); n != 0 {
			t.Errorf("%s still has %d row(s) for the deleted account", tbl, n)
		}
		if n := countWhere(t, st, tbl, "account_jid", kept); n == 0 {
			t.Errorf("%s lost the other account's rows", tbl)
		}
	}
	if n := countWhere(t, st, "whatsmeow_privacy_tokens", "our_jid", gone+":7@s.whatsapp.net"); n != 0 {
		t.Error("privacy tokens of the deleted account survived")
	}
	if n := countWhere(t, st, "whatsmeow_privacy_tokens", "our_jid", kept+":7@s.whatsapp.net"); n == 0 {
		t.Error("privacy tokens of the other account were deleted")
	}
	if err := st.DeleteAccountData(ctx, "55%"); err == nil {
		t.Fatal("a non-digit account must be refused, it would widen the LIKE match")
	}
}

func countWhere(t *testing.T, st *Store, table, col, val string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+col+` = ?`, val).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
