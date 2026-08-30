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
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	PendingTTL = 10 * time.Minute
	CodeTTL    = 60 * time.Second
	RefreshTTL = 30 * 24 * time.Hour
	AccessTTL  = time.Hour
)

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

// Init creates the refresh-token table.
func (s *Store) Init(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
		token      TEXT PRIMARY KEY,
		client_id  TEXT,
		sub        TEXT,
		resource   TEXT,
		scopes     TEXT,
		expires_at BIGINT
	)`)
	return err
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
	p.ExpiresAt = time.Now().Add(PendingTTL)
	s.pending[p.FlowID] = p
	return p.FlowID
}

func (s *Store) Pending(flowID string) (*Pending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[flowID]
	if !ok || time.Now().After(p.ExpiresAt) {
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
		Sub: sub, ExpiresAt: time.Now().Add(CodeTTL),
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
	if time.Now().After(c.ExpiresAt) {
		return nil, false
	}
	return c, true
}

// TakeRefresh consumes a refresh token (single use, from Postgres) and validates
// expiry + that the subject still has a paired device.
func (s *Store) TakeRefresh(ctx context.Context, token string) (*Refresh, bool) {
	var r Refresh
	var scopes string
	var exp int64
	row := s.db.QueryRowContext(ctx,
		`DELETE FROM oauth_refresh_tokens WHERE token=$1
		 RETURNING token, client_id, sub, resource, scopes, expires_at`, token)
	if err := row.Scan(&r.Token, &r.ClientID, &r.Sub, &r.Resource, &scopes, &exp); err != nil {
		return nil, false
	}
	_ = json.Unmarshal([]byte(scopes), &r.Scopes)
	if time.Now().UnixMilli() > exp {
		return nil, false
	}
	if s.hasDevice != nil && !s.hasDevice(r.Sub) {
		return nil, false
	}
	return &r, true
}

func (s *Store) newRefresh(ctx context.Context, clientID, sub, resource string, scopes []string) (string, error) {
	token := randHex(32)
	sc, _ := json.Marshal(scopes)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_refresh_tokens (token, client_id, sub, resource, scopes, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		token, clientID, sub, resource, string(sc), time.Now().Add(RefreshTTL).UnixMilli())
	return token, err
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
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": s.issuer, "aud": resource, "sub": sub,
		"iat": now.Unix(), "exp": now.Add(AccessTTL).Unix(),
		"client_id": clientID, "scope": joinScopes(scopes),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["typ"] = "at+jwt"
	access, err := tok.SignedString(s.jwtKey)
	if err != nil {
		return nil, err
	}
	refresh, err := s.newRefresh(ctx, clientID, sub, resource, scopes)
	if err != nil {
		return nil, err
	}
	return &Tokens{AccessToken: access, RefreshToken: refresh, ExpiresIn: int(AccessTTL.Seconds()), Scope: joinScopes(scopes)}, nil
}

// Verify parses+validates an access token and returns its subject (phone).
func (s *Store) Verify(token string) (string, error) {
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.jwtKey, nil
	}, jwt.WithIssuer(s.issuer), jwt.WithValidMethods([]string{"HS256"}))
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
