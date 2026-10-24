// Package oauth implements the OAuth 2.1 authorization-server pieces the MCP
// HTTP transport needs: PKCE, dynamic client registration, auth-code + refresh
// token lifecycle, and JWT access tokens whose subject is the paired WhatsApp
// phone number.
//
// Persistence: refresh tokens live in Postgres so pod restarts do NOT log users
// out (the access token is a stateless JWT verified by the stable secret, and
// refresh survives). "Paired" is derived from the whatsmeow device store via a
// callback, so a restart that reconnects devices keeps refresh valid. Only the
// short-lived pending flows and auth codes stay in memory (losing them on a
// restart at most forces retry of an in-flight authorize).
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	PendingTTL   = 10 * time.Minute
	CodeTTL      = 60 * time.Second
	RefreshTTL   = 30 * 24 * time.Hour
	AccessTTL    = time.Hour
	RefreshGrace = 120 * time.Second
)

// ErrInvalidGrant is returned when a refresh token is unknown, expired, tied to
// an unlinked device, or replayed outside the rotation grace window.
var ErrInvalidGrant = errors.New("invalid_grant")

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// VerifyPKCE checks a base64url-encoded S256 challenge against a verifier.
func VerifyPKCE(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

// ---- records ----

type Client struct {
	ID           string
	RedirectURIs []string
	Name         string
}

type Pending struct {
	FlowID        string
	ClientID      string
	RedirectURI   string
	State         string
	Scopes        []string
	CodeChallenge string
	Resource      string
	ExpiresAt     time.Time
}

type AuthCode struct {
	Code          string
	ClientID      string
	RedirectURI   string
	CodeChallenge string
	Scopes        []string
	Resource      string
	Sub           string
	ExpiresAt     time.Time
}

type Refresh struct {
	Token    string
	ClientID string
	Sub      string
	Resource string
	Scopes   []string
}

// ---- store ----

// Store holds OAuth state. Refresh tokens are in Postgres; clients/pending/codes
// are in memory (short-lived or cheaply re-created). hasDevice reports whether a
// subject still has a paired WhatsApp device (used to invalidate refresh after a
// logout).
type Store struct {
	mu        sync.Mutex
	clients   map[string]*Client
	pending   map[string]*Pending
	codes     map[string]*AuthCode
	db        *sql.DB
	hasDevice func(sub string) bool
	jwtKey    []byte
	issuer    string
	resource  string
	nowFn     func() time.Time
}

func NewStore(jwtSecret, issuer, resource string, db *sql.DB, hasDevice func(string) bool) *Store {
	return &Store{
		clients:   map[string]*Client{},
		pending:   map[string]*Pending{},
		codes:     map[string]*AuthCode{},
		db:        db,
		hasDevice: hasDevice,
		jwtKey:    []byte(jwtSecret),
		issuer:    issuer,
		resource:  resource,
	}
}

func (s *Store) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// Init creates the refresh-token table and migrates existing databases in place
// to add the rotation-grace and family columns without breaking legacy rows.
func (s *Store) Init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
		token             TEXT PRIMARY KEY,
		client_id         TEXT,
		sub               TEXT,
		resource          TEXT,
		scopes            TEXT,
		expires_at        BIGINT,
		family_id         TEXT,
		consumed_at       BIGINT,
		successor_refresh TEXT,
		successor_access  TEXT
	)`); err != nil {
		return err
	}
	alterCols := []string{
		"family_id TEXT",
		"consumed_at BIGINT",
		"successor_refresh TEXT",
		"successor_access TEXT",
	}
	for _, col := range alterCols {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE oauth_refresh_tokens ADD COLUMN IF NOT EXISTS "+col); err != nil {
			// SQLite does not support IF NOT EXISTS on ADD COLUMN; fall back to
			// plain ADD COLUMN and ignore duplicate-column errors.
			if _, err2 := s.db.ExecContext(ctx, "ALTER TABLE oauth_refresh_tokens ADD COLUMN "+col); err2 != nil && !isDuplicateColumnErr(err2) {
				return err
			}
		}
	}
	_, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_oauth_refresh_family ON oauth_refresh_tokens(family_id)`)
	return err
}

func isDuplicateColumnErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate column") || strings.Contains(msg, "already exists")
}

// RegisterClient implements RFC 7591 dynamic client registration.
func (s *Store) RegisterClient(redirectURIs []string, name string) *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &Client{ID: "wa_" + randHex(12), RedirectURIs: redirectURIs, Name: name}
	s.clients[c.ID] = c
	return c
}

func (s *Store) Client(id string) (*Client, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clients[id]
	return c, ok
}

func (s *Store) NewPending(p *Pending) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.FlowID = randHex(16)
	p.ExpiresAt = s.now().Add(PendingTTL)
	s.pending[p.FlowID] = p
	return p.FlowID
}

func (s *Store) Pending(flowID string) (*Pending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[flowID]
	if !ok || s.now().After(p.ExpiresAt) {
		delete(s.pending, flowID)
		return nil, false
	}
	return p, true
}

func (s *Store) DeletePending(flowID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, flowID)
}

// IssueCode mints a single-use auth code once pairing captured a phone (sub).
func (s *Store) IssueCode(p *Pending, sub string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	code := randHex(24)
	s.codes[code] = &AuthCode{
		Code: code, ClientID: p.ClientID, RedirectURI: p.RedirectURI,
		CodeChallenge: p.CodeChallenge, Scopes: p.Scopes, Resource: p.Resource,
		Sub: sub, ExpiresAt: s.now().Add(CodeTTL),
	}
	return code
}

// TakeCode consumes an auth code (single use).
func (s *Store) TakeCode(code string) (*AuthCode, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.codes[code]
	if !ok {
		return nil, false
	}
	delete(s.codes, code)
	if s.now().After(c.ExpiresAt) {
		return nil, false
	}
	return c, true
}

// RotateRefresh consumes a refresh token with a 120s rotation grace window and
// family-wide reuse detection:
//   - First consumption mints a successor (access + refresh) in the same family,
//     marks the token consumed_at, and stores the successor pair on the row.
//   - Re-presenting the SAME refresh token within RefreshGrace (120s) returns
//     the stored successor pair idempotently, so concurrent processes racing on
//     a single refresh token all receive the same successor without invalid_grant.
//   - Re-presenting a consumed refresh token after RefreshGrace treats it as
//     token reuse, revokes the entire token family, and returns ErrInvalidGrant.
func (s *Store) RotateRefresh(ctx context.Context, token string) (*Tokens, error) {
	if token == "" {
		return nil, ErrInvalidGrant
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		r                Refresh
		scopesJSON       string
		exp              int64
		familyID         string
		consumedAt       int64
		successorRefresh string
		successorAccess  string
	)
	row := s.db.QueryRowContext(ctx,
		`SELECT token, COALESCE(client_id,''), COALESCE(sub,''), COALESCE(resource,''),
		        COALESCE(scopes,''), COALESCE(expires_at,0),
		        COALESCE(family_id,''), COALESCE(consumed_at,0),
		        COALESCE(successor_refresh,''), COALESCE(successor_access,'')
		 FROM oauth_refresh_tokens WHERE token=$1`, token)
	if err := row.Scan(
		&r.Token, &r.ClientID, &r.Sub, &r.Resource,
		&scopesJSON, &exp,
		&familyID, &consumedAt,
		&successorRefresh, &successorAccess,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidGrant
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(scopesJSON), &r.Scopes)
	if familyID == "" {
		familyID = r.Token
	}

	now := s.now()
	nowMs := now.UnixMilli()

	if consumedAt > 0 {
		age := nowMs - consumedAt
		if age < 0 || age > RefreshGrace.Milliseconds() || successorRefresh == "" || successorAccess == "" {
			_ = s.revokeFamily(ctx, familyID)
			return nil, ErrInvalidGrant
		}
		if s.hasDevice != nil && !s.hasDevice(r.Sub) {
			_ = s.revokeFamily(ctx, familyID)
			return nil, ErrInvalidGrant
		}
		return &Tokens{
			AccessToken:  successorAccess,
			RefreshToken: successorRefresh,
			ExpiresIn:    int(AccessTTL.Seconds()),
			Scope:        joinScopes(r.Scopes),
		}, nil
	}

	if nowMs > exp {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE token=$1`, r.Token)
		return nil, ErrInvalidGrant
	}
	if s.hasDevice != nil && !s.hasDevice(r.Sub) {
		_ = s.revokeFamily(ctx, familyID)
		return nil, ErrInvalidGrant
	}

	resource := r.Resource
	if resource == "" {
		resource = s.resource
	}
	access, err := s.mintAccess(now, r.ClientID, r.Sub, resource, r.Scopes)
	if err != nil {
		return nil, err
	}
	newRefresh := randHex(32)
	sc, _ := json.Marshal(r.Scopes)
	newExp := now.Add(RefreshTTL).UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO oauth_refresh_tokens
		 (token, client_id, sub, resource, scopes, expires_at, family_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		newRefresh, r.ClientID, r.Sub, resource, string(sc), newExp, familyID,
	); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE oauth_refresh_tokens
		 SET consumed_at=$1, successor_refresh=$2, successor_access=$3, family_id=$4
		 WHERE token=$5`,
		nowMs, newRefresh, access, familyID, r.Token,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &Tokens{
		AccessToken:  access,
		RefreshToken: newRefresh,
		ExpiresIn:    int(AccessTTL.Seconds()),
		Scope:        joinScopes(r.Scopes),
	}, nil
}

func (s *Store) revokeFamily(ctx context.Context, familyID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM oauth_refresh_tokens WHERE family_id=$1 OR token=$1`, familyID)
	return err
}

func (s *Store) newRefresh(ctx context.Context, now time.Time, clientID, sub, resource string, scopes []string) (string, error) {
	token := randHex(32)
	sc, _ := json.Marshal(scopes)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_refresh_tokens (token, client_id, sub, resource, scopes, expires_at, family_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		token, clientID, sub, resource, string(sc), now.Add(RefreshTTL).UnixMilli(), token)
	return token, err
}

func (s *Store) mintAccess(now time.Time, clientID, sub, resource string, scopes []string) (string, error) {
	if resource == "" {
		resource = s.resource
	}
	claims := jwt.MapClaims{
		"iss": s.issuer, "aud": resource, "sub": sub,
		"iat": now.Unix(), "exp": now.Add(AccessTTL).Unix(),
		"jti":       randHex(16),
		"client_id": clientID, "scope": joinScopes(scopes),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["typ"] = "at+jwt"
	return tok.SignedString(s.jwtKey)
}

// Tokens is an issued token pair.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Scope        string
}

// Issue mints a JWT access token (sub = phone) plus a persisted refresh token.
func (s *Store) Issue(ctx context.Context, clientID, sub, resource string, scopes []string) (*Tokens, error) {
	if resource == "" {
		resource = s.resource
	}
	now := s.now()
	access, err := s.mintAccess(now, clientID, sub, resource, scopes)
	if err != nil {
		return nil, err
	}
	refresh, err := s.newRefresh(ctx, now, clientID, sub, resource, scopes)
	if err != nil {
		return nil, err
	}
	return &Tokens{AccessToken: access, RefreshToken: refresh, ExpiresIn: int(AccessTTL.Seconds()), Scope: joinScopes(scopes)}, nil
}

// Verify parses+validates an access token and returns its subject (phone).
func (s *Store) Verify(token string) (string, error) {
	opts := []jwt.ParserOption{
		jwt.WithIssuer(s.issuer),
		jwt.WithValidMethods([]string{"HS256"}),
	}
	if s.nowFn != nil {
		opts = append(opts, jwt.WithTimeFunc(s.nowFn))
	}
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.jwtKey, nil
	}, opts...)
	if err != nil {
		return "", err
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return "", errors.New("invalid token")
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", errors.New("missing sub")
	}
	return sub, nil
}

func joinScopes(scopes []string) string {
	out := ""
	for i, s := range scopes {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}
