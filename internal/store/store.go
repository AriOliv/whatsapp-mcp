// Package store is our own message/chat history — whatsmeow keeps none, so we
// persist what we need from events (events.Message / events.HistorySync) to back
// the find_chats / find_messages / find_contacts tools.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"  // postgres driver ("postgres")
	_ "modernc.org/sqlite" // pure-Go sqlite driver ("sqlite"), no CGO
)

// Store wraps a *sql.DB and knows which dialect it is (for placeholder rebinding).
type Store struct {
	db   *sql.DB
	isPG bool
}

// Pool limits for Postgres. The database allows 400 connections and is shared
// with other services, so these are sized for headroom rather than to the cap:
// eight linked accounts replaying history, plus this server's own queries, stay
// comfortably inside them.
const (
	// waMaxOpenConns is the larger share because whatsmeow needs a connection
	// inside the code path that receives messages.
	waMaxOpenConns  = 40
	waMaxIdleConns  = 10
	appMaxOpenConns = 15
	appMaxIdleConns = 5

	connMaxLifetime = 30 * time.Minute
	connMaxIdleTime = 5 * time.Minute
)

// Open opens the application store and the handle whatsmeow will use.
//
// On Postgres those are two separate pools over the same database, deliberately.
// whatsmeow needs a connection inside its serial node handler — the code path
// that receives messages — so a single shared pool lets this server's own
// queries starve message reception: exhaust it with tool calls and whatsmeow
// stops answering the socket until one frees up. Separate pools mean neither
// side can take the other down, and each is sized for its own work.
//
// SQLite keeps one handle: there is no connection limit to divide, and a second
// pool would only add file-lock contention.
func Open(dbURL string, isPG bool) (*Store, *sql.DB, error) {
	driver := "sqlite"
	if isPG {
		driver = "postgres"
	}
	appDB, err := openPool(driver, dbURL, isPG, appMaxOpenConns, appMaxIdleConns)
	if err != nil {
		return nil, nil, err
	}
	st := &Store{db: appDB, isPG: isPG}
	if !isPG {
		return st, appDB, nil
	}
	waDB, err := openPool(driver, dbURL, isPG, waMaxOpenConns, waMaxIdleConns)
	if err != nil {
		return nil, nil, err
	}
	return st, waDB, nil
}

// openPool opens one connection pool and verifies it can reach the database.
func openPool(driver, dsn string, bound bool, maxOpen, maxIdle int) (*sql.DB, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", driver, err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping %s: %w", driver, err)
	}
	if bound {
		db.SetMaxOpenConns(maxOpen)
		db.SetMaxIdleConns(maxIdle)
		db.SetConnMaxLifetime(connMaxLifetime)
		db.SetConnMaxIdleTime(connMaxIdleTime)
	}
	return db, nil
}

// SaveChatNames records display names learned for chats that already exist, so
// later listings can render them straight from the database instead of paying a
// WhatsApp round-trip. Chats with no stored messages are skipped — they would
// not show up in a listing anyway — which keeps this write proportional to what
// was actually displayed rather than to the size of the account.
func (s *Store) SaveChatNames(ctx context.Context, account string, names map[string]string) error {
	if len(names) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed
	stmt, err := tx.PrepareContext(ctx, s.reb(
		`UPDATE chats SET name=? WHERE account_jid=? AND jid=? AND COALESCE(name,'')<>?`))
	if err != nil {
		return err
	}
	defer stmt.Close()
	for jid, name := range names {
		if name == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, name, account, jid, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ChatName returns the stored display name of one chat, or "" when the chat is
// unknown or still unnamed.
func (s *Store) ChatName(ctx context.Context, account, jid string) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, s.reb(
		`SELECT COALESCE(name,'') FROM chats WHERE account_jid=? AND jid=?`), account, jid).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
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
	if err := s.ensureMediaColumn(ctx); err != nil {
		return fmt.Errorf("init schema (media column): %w", err)
	}
	return nil
}

// ensureMediaColumn adds the `media` column (serialized message proto for media
// messages) to the messages table if it isn't there yet — idempotent on both
// Postgres and SQLite so it upgrades pre-existing databases in place.
func (s *Store) ensureMediaColumn(ctx context.Context) error {
	if s.isPG {
		_, err := s.db.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN IF NOT EXISTS media BYTEA`)
		return err
	}
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(messages)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	has := false
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "media" {
			has = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !has {
		_, err = s.db.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN media BLOB`)
		return err
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
	// MediaProto is the serialized whatsmeow message proto (carries the media
	// keys) for media messages, used by DownloadMedia. Never serialized to JSON.
	MediaProto []byte `json:"-"`
}

// Chat is one stored chat row.
type Chat struct {
	JID    string `json:"jid"`
	Name   string `json:"name"`
	LastTS int64  `json:"last_ts_millis"`
}

// SaveMessage upserts a message and bumps its chat's last-activity timestamp.
func (s *Store) SaveMessage(ctx context.Context, account string, m Message) error {
	up := `INSERT INTO messages (account_jid,id,chat_jid,sender_jid,from_me,ts,body,media_type,media)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (account_jid,id) DO UPDATE SET body=excluded.body, media_type=excluded.media_type,
			media=COALESCE(excluded.media, messages.media)`
	if _, err := s.db.ExecContext(ctx, s.reb(up),
		account, m.ID, m.ChatJID, m.SenderJID, m.FromMe, m.TS, m.Body, m.MediaType, m.MediaProto); err != nil {
		return err
	}
	ch := `INSERT INTO chats (account_jid,jid,last_ts) VALUES (?,?,?)
		ON CONFLICT (account_jid,jid) DO UPDATE SET last_ts=excluded.last_ts WHERE excluded.last_ts > chats.last_ts`
	_, err := s.db.ExecContext(ctx, s.reb(ch), account, m.ChatJID, m.TS)
	return err
}

// GetMedia returns the stored media proto (and media_type) for a message by ID.
// A nil/empty result means the message has no downloadable media stored.
func (s *Store) GetMedia(ctx context.Context, account, msgID string) ([]byte, string, error) {
	var (
		media []byte
		mtype sql.NullString
	)
	err := s.db.QueryRowContext(ctx, s.reb(
		`SELECT media, COALESCE(media_type,'') FROM messages WHERE account_jid=? AND id=?`),
		account, msgID).Scan(&media, &mtype)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return media, mtype.String, nil
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

// LatestPerChat returns the most recent message of each of the given chats in a
// single query, so a chat list can render preview lines without one round-trip
// per row.
func (s *Store) LatestPerChat(ctx context.Context, account string, chatJIDs []string) (map[string]Message, error) {
	out := make(map[string]Message, len(chatJIDs))
	if len(chatJIDs) == 0 {
		return out, nil
	}
	// Build an IN list with placeholders; reb() renumbers them for Postgres.
	ph := make([]string, len(chatJIDs))
	args := make([]any, 0, len(chatJIDs)+1)
	args = append(args, account)
	for i, j := range chatJIDs {
		ph[i] = "?"
		args = append(args, j)
	}
	q := `SELECT m.id, m.chat_jid, COALESCE(m.sender_jid,''), m.from_me, COALESCE(m.ts,0),
	             COALESCE(m.body,''), COALESCE(m.media_type,'')
	      FROM messages m
	      JOIN (SELECT chat_jid, MAX(ts) AS ts FROM messages
	            WHERE account_jid=? AND chat_jid IN (` + strings.Join(ph, ",") + `)
	            GROUP BY chat_jid) latest
	        ON latest.chat_jid = m.chat_jid AND latest.ts = m.ts
	      WHERE m.account_jid = ?`
	args = append(args, account)
	rows, err := s.db.QueryContext(ctx, s.reb(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ChatJID, &m.SenderJID, &m.FromMe, &m.TS, &m.Body, &m.MediaType); err != nil {
			return nil, err
		}
		out[m.ChatJID] = m
	}
	return out, rows.Err()
}

// AllLIDMappings returns whatsmeow's full LID→phone map (user parts only) in a
// single query, so callers can resolve @lid contacts to phone numbers in memory
// instead of one round-trip per contact. Reads whatsmeow's shared lid-map table;
// returns an empty map (not an error) if the table isn't present yet.
func (s *Store) AllLIDMappings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT lid, pn FROM whatsmeow_lid_map`)
	if err != nil {
		return map[string]string{}, nil
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var lid, pn string
		if err := rows.Scan(&lid, &pn); err != nil {
			return out, err
		}
		out[lid] = pn
	}
	return out, rows.Err()
}

// DB exposes the underlying handle (for sharing with whatsmeow's Postgres store).
func (s *Store) DB() *sql.DB { return s.db }
