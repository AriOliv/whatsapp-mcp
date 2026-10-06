package store

// Web forms: a form is sent as a link button; the recipient fills it on a page
// served by this process and the answers come back out of band (never into the
// chat). Only a hash of the link token is stored.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Form statuses.
const (
	FormOpen      = "open"
	FormSubmitted = "submitted"
)

// ErrFormClosed is returned by SubmitForm when the form was already submitted
// or has expired.
var ErrFormClosed = errors.New("form is closed")

// Form is one sent form.
type Form struct {
	ID          string
	TokenHash   string
	Account     string
	To          string
	Spec        []byte // JSON
	Status      string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	SubmittedAt time.Time
	Answers     []byte // JSON, set once submitted
}

// Expired reports whether an open form is past its deadline.
func (f *Form) Expired(now time.Time) bool { return f.Status == FormOpen && !now.Before(f.ExpiresAt) }

// InitForms creates the forms table (idempotent).
func (s *Store) InitForms(ctx context.Context) error {
	blob := "BLOB"
	if s.isPG {
		blob = "BYTEA"
	}
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS forms (
			id           TEXT PRIMARY KEY,
			token_hash   TEXT NOT NULL UNIQUE,
			account_jid  TEXT NOT NULL,
			to_jid       TEXT NOT NULL,
			spec         %[1]s NOT NULL,
			status       TEXT NOT NULL,
			created_at   BIGINT NOT NULL,
			expires_at   BIGINT NOT NULL,
			submitted_at BIGINT NOT NULL DEFAULT 0,
			answers      %[1]s
		)`, blob),
		`CREATE INDEX IF NOT EXISTS idx_forms_expires ON forms(expires_at)`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("init forms: %w", err)
		}
	}
	return nil
}

// CreateForm stores a new open form.
func (s *Store) CreateForm(ctx context.Context, f Form) error {
	_, err := s.db.ExecContext(ctx, s.reb(`INSERT INTO forms
		(id, token_hash, account_jid, to_jid, spec, status, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		f.ID, f.TokenHash, f.Account, f.To, f.Spec, FormOpen, f.CreatedAt.UnixMilli(), f.ExpiresAt.UnixMilli())
	return err
}

const formCols = `id, token_hash, account_jid, to_jid, spec, status, created_at, expires_at, submitted_at, answers`

func scanForm(row interface{ Scan(...any) error }) (*Form, error) {
	var f Form
	var created, expires, submitted int64
	if err := row.Scan(&f.ID, &f.TokenHash, &f.Account, &f.To, &f.Spec, &f.Status, &created, &expires, &submitted, &f.Answers); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	f.CreatedAt, f.ExpiresAt = time.UnixMilli(created), time.UnixMilli(expires)
	if submitted > 0 {
		f.SubmittedAt = time.UnixMilli(submitted)
	}
	return &f, nil
}

// FormByToken looks a form up by the hash of its link token; nil if unknown.
func (s *Store) FormByToken(ctx context.Context, tokenHash string) (*Form, error) {
	return scanForm(s.db.QueryRowContext(ctx, s.reb(`SELECT `+formCols+` FROM forms WHERE token_hash = ?`), tokenHash))
}

// GetForm returns one form of an account; nil if unknown (or another account's).
func (s *Store) GetForm(ctx context.Context, account, id string) (*Form, error) {
	return scanForm(s.db.QueryRowContext(ctx, s.reb(`SELECT `+formCols+` FROM forms WHERE account_jid = ? AND id = ?`), account, id))
}

// SubmitForm records the answers once: it fails with ErrFormClosed if the form
// was already submitted or has expired.
func (s *Store) SubmitForm(ctx context.Context, id string, answers []byte, now time.Time) error {
	r, err := s.db.ExecContext(ctx, s.reb(`UPDATE forms SET status = ?, answers = ?, submitted_at = ?
		WHERE id = ? AND status = ? AND expires_at > ?`),
		FormSubmitted, answers, now.UnixMilli(), id, FormOpen, now.UnixMilli())
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrFormClosed
	}
	return nil
}

// PurgeForms deletes forms (and their answers) whose submission or deadline is
// older than keep.
func (s *Store) PurgeForms(ctx context.Context, keep time.Duration, now time.Time) (int64, error) {
	cut := now.Add(-keep).UnixMilli()
	r, err := s.db.ExecContext(ctx, s.reb(`DELETE FROM forms
		WHERE (status = ? AND submitted_at < ?) OR (status = ? AND expires_at < ?)`),
		FormSubmitted, cut, FormOpen, cut)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}
