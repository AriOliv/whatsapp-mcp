// Package gateway implements the "gateway" auth mode: the server sits behind
// an MCP gateway that authenticates users and forwards each request with a
// short-lived RS256 JWT naming the user. This package verifies that JWT,
// manages owner-to-number bindings and pairing, and builds the HTTP surface.
package gateway

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

// clockLeeway absorbs small clock drift between the gateway and this server.
const clockLeeway = 30 * time.Second

// KeySource resolves a JWT signing key by kid.
type KeySource interface {
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// Verifier checks gateway-issued user JWTs.
type Verifier struct {
	keys     KeySource
	issuer   string
	audience string
	domains  map[string]bool
	now      func() time.Time
}

// NewVerifier requires an exact issuer and audience, and an email subject in
// one of domains.
func NewVerifier(keys KeySource, issuer, audience string, domains []string) (*Verifier, error) {
	if issuer == "" || audience == "" {
		return nil, errors.New("gateway: issuer and audience are required")
	}
	set := map[string]bool{}
	for _, d := range domains {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			set[d] = true
		}
	}
	if len(set) == 0 {
		return nil, errors.New("gateway: at least one allowed email domain is required")
	}
	return &Verifier{keys: keys, issuer: issuer, audience: audience, domains: set, now: time.Now}, nil
}

// Identity is what a verified token says about its caller.
type Identity struct {
	Email   string
	Expires time.Time
	Scopes  []string
}

type claims struct {
	jwt.RegisteredClaims
	Scope string `json:"scope"`
}

// Verify validates raw and returns the caller's identity.
func (v *Verifier) Verify(ctx context.Context, raw string) (Identity, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(clockLeeway),
		jwt.WithTimeFunc(v.now),
	)
	var c claims
	_, err := parser.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("token has no kid")
		}
		return v.keys.Key(ctx, kid)
	})
	if err != nil {
		return Identity{}, err
	}
	email, err := v.ownerEmail(c.Subject)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Email: email, Expires: c.ExpiresAt.Time, Scopes: strings.Fields(c.Scope)}, nil
}

// ownerEmail accepts sub only if it is a bare address in an allowed domain.
func (v *Verifier) ownerEmail(sub string) (string, error) {
	addr, err := mail.ParseAddress(sub)
	if err != nil || addr.Name != "" || addr.Address != sub {
		return "", errors.New("sub is not an email address")
	}
	email := strings.ToLower(addr.Address)
	at := strings.LastIndexByte(email, '@')
	if at <= 0 || !v.domains[email[at+1:]] {
		return "", errors.New("email domain not allowed")
	}
	return email, nil
}

// TokenVerifier adapts Verify to the MCP SDK's bearer middleware. Every
// failure is reported as an invalid token; the reason is only logged.
func (v *Verifier) TokenVerifier() auth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		id, err := v.Verify(ctx, token)
		if err != nil {
			log.Printf("gateway: rejected token: %v", err)
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: id.Email, Expiration: id.Expires, Scopes: id.Scopes}, nil
	}
}

// ParseDomains splits a comma-separated domain list.
func ParseDomains(csv string) []string {
	var out []string
	for _, d := range strings.Split(csv, ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			out = append(out, d)
		}
	}
	return out
}
