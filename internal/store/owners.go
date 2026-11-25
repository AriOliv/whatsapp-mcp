package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Owner bindings tie a gateway-authenticated owner (an email) to the one
// WhatsApp account (phone digits) that owner may act on. Both sides are unique:
// an owner has at most one number and a number belongs to at most one owner.

var (
	ErrOwnerBound   = errors.New("owner already has a WhatsApp number connected")
	ErrAccountBound = errors.New("WhatsApp number is already connected by another user")
)

// InitOwners creates the owner_bindings table (idempotent).
func (s *Store) InitOwners(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS owner_bindings (
		owner      TEXT PRIMARY KEY,
		account    TEXT NOT NULL UNIQUE,
		created_at BIGINT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("init owner bindings: %w", err)
	}
	return nil
}

// AccountOf returns the account bound to owner, or "" when there is none.
func (s *Store) AccountOf(ctx context.Context, owner string) (string, error) {
	return s.lookupBinding(ctx, `SELECT account FROM owner_bindings WHERE owner=?`, owner)
}

// OwnerOf returns the owner an account is bound to, or "" when there is none.
func (s *Store) OwnerOf(ctx context.Context, account string) (string, error) {
	return s.lookupBinding(ctx, `SELECT owner FROM owner_bindings WHERE account=?`, account)
}

func (s *Store) lookupBinding(ctx context.Context, q, arg string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, s.reb(q), arg).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// Bind records that owner controls account. Binding the same pair again is a
// no-op; any other conflict returns ErrOwnerBound or ErrAccountBound.
func (s *Store) Bind(ctx context.Context, owner, account string) error {
	if owner == "" || account == "" {
		return errors.New("bind: owner and account are required")
	}
	res, err := s.db.ExecContext(ctx, s.reb(
		`INSERT INTO owner_bindings (owner, account, created_at) VALUES (?,?,?) ON CONFLICT DO NOTHING`),
		owner, account, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("bind: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	current, err := s.AccountOf(ctx, owner)
	if err != nil {
		return fmt.Errorf("bind: %w", err)
	}
	switch current {
	case account:
		return nil
	case "":
		return ErrAccountBound
	default:
		return ErrOwnerBound
	}
}

// Unbind removes owner's binding, if any.
func (s *Store) Unbind(ctx context.Context, owner string) error {
	_, err := s.db.ExecContext(ctx, s.reb(`DELETE FROM owner_bindings WHERE owner=?`), owner)
	return err
}

// DeleteAccountData removes everything this server stored for account: history,
// chats, forms, pending webhook deliveries, the logout marker, and whatsmeow's
// privacy tokens (the only whatsmeow table not cleared by deleting the device
// row). account must be bare phone digits.
func (s *Store) DeleteAccountData(ctx context.Context, account string) error {
	if !isDigits(account) {
		return fmt.Errorf("delete account data: invalid account %q", account)
	}
	jid := account + "@s.whatsapp.net"
	stmts := []struct {
		q    string
		args []any
	}{
		{`DELETE FROM messages WHERE account_jid IN (?,?)`, []any{account, jid}},
		{`DELETE FROM chats WHERE account_jid IN (?,?)`, []any{account, jid}},
		{`DELETE FROM forms WHERE account_jid IN (?,?)`, []any{account, jid}},
		{`DELETE FROM webhook_outbox WHERE account_jid IN (?,?)`, []any{account, jid}},
		{`DELETE FROM logged_out_accounts WHERE account_jid IN (?,?)`, []any{account, jid}},
		{`DELETE FROM whatsmeow_privacy_tokens WHERE our_jid = ? OR our_jid LIKE ?`, []any{jid, account + ":%"}},
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, st := range stmts {
		if _, err := tx.ExecContext(ctx, s.reb(st.q), st.args...); err != nil {
			return fmt.Errorf("delete account data: %w", err)
		}
	}
	return tx.Commit()
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
