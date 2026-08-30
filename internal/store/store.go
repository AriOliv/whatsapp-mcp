// Package store is our own message/chat history — whatsmeow keeps none, so we
// persist what we need from events (events.Message / events.HistorySync) to back
// the find_chats / find_messages / find_contacts tools.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/lib/pq"  // postgres driver ("postgres")
	_ "modernc.org/sqlite" // pure-Go sqlite driver ("sqlite"), no CGO
)

// Store wraps a *sql.DB and knows which dialect it is (for placeholder rebinding).
type Store struct {
	db   *sql.DB
	isPG bool
}

// Open opens the application store DB. Postgres when dbURL is a postgres URL,
// otherwise a modernc SQLite DSN. Returns the *sql.DB too so the caller can share
// it with the whatsmeow sqlstore when using Postgres.
func Open(dbURL string, isPG bool) (*Store, *sql.DB, error) {
	driver, dsn := "sqlite", dbURL
	if isPG {
		driver, dsn = "postgres", dbURL
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", driver, err)
	}
	if err := db.Ping(); err != nil {
		return nil, nil, fmt.Errorf("ping %s: %w", driver, err)
	}
	return &Store{db: db, isPG: isPG}, db, nil
}

// reb rewrites ? placeholders to $1,$2,... for Postgres; SQLite keeps ?.
func (s *Store) reb(q string) string {
	if !s.isPG {
		return q
	}
	var b strings.Builder
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
			b.WriteString("$")
			fmt.Fprintf(&b, "%d", n)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Init creates the schema if absent.
func (s *Store) Init(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS chats (
			account_jid TEXT NOT NULL,
			jid         TEXT NOT NULL,
			name        TEXT,
			last_ts     BIGINT,
			PRIMARY KEY (account_jid, jid)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			account_jid TEXT NOT NULL,
			id          TEXT NOT NULL,
			chat_jid    TEXT NOT NULL,
			sender_jid  TEXT,
			from_me     BOOLEAN,
			ts          BIGINT,
			body        TEXT,
			media_type  TEXT,
			PRIMARY KEY (account_jid, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat ON messages(account_jid, chat_jid, ts)`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("init schema: %w", err)
		}
	}
	return nil
}

// Message is one stored message row.
type Message struct {
	ID        string `json:"id"`
	ChatJID   string `json:"chat_jid"`
	SenderJID string `json:"sender_jid"`
	FromMe    bool   `json:"from_me"`
	TS        int64  `json:"ts_millis"`
	Body      string `json:"body"`
	MediaType string `json:"media_type,omitempty"`
}

// Chat is one stored chat row.
type Chat struct {
	JID    string `json:"jid"`
	Name   string `json:"name"`
	LastTS int64  `json:"last_ts_millis"`
}

// SaveMessage upserts a message and bumps its chat's last-activity timestamp.
func (s *Store) SaveMessage(ctx context.Context, account string, m Message) error {
	up := `INSERT INTO messages (account_jid,id,chat_jid,sender_jid,from_me,ts,body,media_type)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT (account_jid,id) DO UPDATE SET body=excluded.body, media_type=excluded.media_type`
	if _, err := s.db.ExecContext(ctx, s.reb(up),
		account, m.ID, m.ChatJID, m.SenderJID, m.FromMe, m.TS, m.Body, m.MediaType); err != nil {
		return err
	}
	ch := `INSERT INTO chats (account_jid,jid,last_ts) VALUES (?,?,?)
		ON CONFLICT (account_jid,jid) DO UPDATE SET last_ts=excluded.last_ts WHERE excluded.last_ts > chats.last_ts`
	_, err := s.db.ExecContext(ctx, s.reb(ch), account, m.ChatJID, m.TS)
	return err
}

// ListChats returns chats for an account, most-recent first.
func (s *Store) ListChats(ctx context.Context, account string, limit int) ([]Chat, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, s.reb(
		`SELECT jid, COALESCE(name,''), COALESCE(last_ts,0) FROM chats WHERE account_jid=? ORDER BY last_ts DESC LIMIT ?`),
		account, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.JID, &c.Name, &c.LastTS); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListMessages returns messages in a chat, most-recent first.
func (s *Store) ListMessages(ctx context.Context, account, chatJID string, limit int) ([]Message, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, s.reb(
		`SELECT id,chat_jid,COALESCE(sender_jid,''),from_me,COALESCE(ts,0),COALESCE(body,''),COALESCE(media_type,'')
		 FROM messages WHERE account_jid=? AND chat_jid=? ORDER BY ts DESC LIMIT ?`),
		account, chatJID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ChatJID, &m.SenderJID, &m.FromMe, &m.TS, &m.Body, &m.MediaType); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DB exposes the underlying handle (for sharing with whatsmeow's Postgres store).
func (s *Store) DB() *sql.DB { return s.db }
