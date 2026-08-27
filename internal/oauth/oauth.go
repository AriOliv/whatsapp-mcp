// Package oauth implements the OAuth 2.1 authorization-server pieces the MCP
// HTTP transport needs: PKCE, dynamic client registration, auth-code + refresh
// token lifecycle, and JWT access tokens whose subject is the paired WhatsApp
// phone number. The QR/code pairing that establishes that subject lives in the
// HTTP handlers (see server.go), which drive wa.Manager.
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TTLs mirror the original server's semantics.
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
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return got == challenge
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
	Sub           string // paired phone number
	ExpiresAt     time.Time
}

type Refresh struct {
	Token     string
	ClientID  string
	Sub       string
	Resource  string
	Scopes    []string
	ExpiresAt time.Time
}

// ---- store ----

// Store holds all OAuth state in memory (durable state is the whatsmeow device
// store; a lost token just means re-pair). Safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	clients  map[string]*Client
	pending  map[string]*Pending
	codes    map[string]*AuthCode
	refresh  map[string]*Refresh
	paired   map[string]bool // sub -> paired (cleared on logout)
	jwtKey   []byte
	issuer   string
	resource string
}

func NewStore(jwtSecret, issuer, resource string) *Store {
	return &Store{
		clients:  map[string]*Client{},
		pending:  map[string]*Pending{},
		codes:    map[string]*AuthCode{},
		refresh:  map[string]*Refresh{},
		paired:   map[string]bool{},
		jwtKey:   []byte(jwtSecret),
		issuer:   issuer,
		resource: resource,
	}
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

// NewPending parks an authorize request; returns the flow id for the login page.
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
	s.paired[sub] = true
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

// TakeRefresh consumes a refresh token (single use + rotated by the caller).
func (s *Store) TakeRefresh(token string) (*Refresh, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.refresh[token]
	if !ok {
		return nil, false
	}
	delete(s.refresh, token)
	if time.Now().After(r.ExpiresAt) || !s.paired[r.Sub] {
		return nil, false
	}
	return r, true
}

func (s *Store) newRefresh(clientID, sub, resource string, scopes []string) string {
	token := randHex(32)
	s.refresh[token] = &Refresh{
		Token: token, ClientID: clientID, Sub: sub, Resource: resource,
		Scopes: scopes, ExpiresAt: time.Now().Add(RefreshTTL),
	}
	return token
}

// Unpair drops a subject (called on logout) so its refresh tokens stop working.
func (s *Store) Unpair(sub string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.paired, sub)
}

// Tokens is an issued token pair.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Scope        string
}

// Issue mints a JWT access token (sub = phone) plus a rotated refresh token.
func (s *Store) Issue(clientID, sub, resource string, scopes []string) (*Tokens, error) {
	if resource == "" {
		resource = s.resource
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":       s.issuer,
		"aud":       resource,
		"sub":       sub,
		"iat":       now.Unix(),
		"exp":       now.Add(AccessTTL).Unix(),
		"client_id": clientID,
		"scope":     joinScopes(scopes),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["typ"] = "at+jwt"
	access, err := tok.SignedString(s.jwtKey)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	refresh := s.newRefresh(clientID, sub, resource, scopes)
	s.mu.Unlock()
	return &Tokens{AccessToken: access, RefreshToken: refresh, ExpiresIn: int(AccessTTL.Seconds()), Scope: joinScopes(scopes)}, nil
}

// Verify parses+validates an access token and returns its subject (phone).
func (s *Store) Verify(token string) (sub string, err error) {
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
	sub, _ = claims["sub"].(string)
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
