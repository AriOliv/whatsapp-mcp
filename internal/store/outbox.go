package store

import (
	"context"
	"fmt"
	"time"
)

// Outbox persists inbound-webhook deliveries so a restart or an outage of the
// receiver does not lose messages. One row per (account, message id).

// OutboxItem is one pending webhook delivery.
type OutboxItem struct {
	ID       int64
	Account  string
	MsgID    string
	Payload  []byte
	Attempts int
}

// InitOutbox creates the outbox table (idempotent).
func (s *Store) InitOutbox(ctx context.Context) error {
	pk := "id INTEGER PRIMARY KEY AUTOINCREMENT"
	blob := "BLOB"
	if s.isPG {
		pk = "id BIGSERIAL PRIMARY KEY"
		blob = "BYTEA"
	}
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS webhook_outbox (
			%s,
			account_jid TEXT NOT NULL,
			msg_id      TEXT NOT NULL,
			payload     %s NOT NULL,
			attempts    INTEGER NOT NULL DEFAULT 0,
			next_at     BIGINT NOT NULL,
			created_at  BIGINT NOT NULL,
			UNIQUE (account_jid, msg_id)
		)`, pk, blob),
		`CREATE INDEX IF NOT EXISTS idx_outbox_next ON webhook_outbox(next_at)`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("init outbox: %w", err)
		}
	}
	return nil
}

// EnqueueOutbox stores a delivery; duplicates (same account+msg) are ignored.
func (s *Store) EnqueueOutbox(ctx context.Context, account, msgID string, payload []byte) error {
	now := time.Now().UnixMilli()
	q := `INSERT INTO webhook_outbox (account_jid, msg_id, payload, attempts, next_at, created_at)
	      VALUES (?, ?, ?, 0, ?, ?) ON CONFLICT (account_jid, msg_id) DO NOTHING`
	_, err := s.db.ExecContext(ctx, s.reb(q), account, msgID, payload, now, now)
	return err
}

// DueOutbox returns up to limit deliveries whose next attempt is due.
func (s *Store) DueOutbox(ctx context.Context, limit int) ([]OutboxItem, error) {
	rows, err := s.db.QueryContext(ctx, s.reb(
		`SELECT id, account_jid, msg_id, payload, attempts FROM webhook_outbox WHERE next_at <= ? ORDER BY id LIMIT ?`),
		time.Now().UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxItem
	for rows.Next() {
		var it OutboxItem
		if err := rows.Scan(&it.ID, &it.Account, &it.MsgID, &it.Payload, &it.Attempts); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// AckOutbox removes a delivered item.
func (s *Store) AckOutbox(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, s.reb(`DELETE FROM webhook_outbox WHERE id = ?`), id)
	return err
}

// RetryOutbox schedules the next attempt.
func (s *Store) RetryOutbox(ctx context.Context, id int64, attempts int, next time.Time) error {
	_, err := s.db.ExecContext(ctx, s.reb(`UPDATE webhook_outbox SET attempts = ?, next_at = ? WHERE id = ?`),
		attempts, next.UnixMilli(), id)
	return err
}

// PurgeOutbox drops items older than maxAge (receiver gone for too long).
func (s *Store) PurgeOutbox(ctx context.Context, maxAge time.Duration) (int64, error) {
	r, err := s.db.ExecContext(ctx, s.reb(`DELETE FROM webhook_outbox WHERE created_at < ?`),
		time.Now().Add(-maxAge).UnixMilli())
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}
