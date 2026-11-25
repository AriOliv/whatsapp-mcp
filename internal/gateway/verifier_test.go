package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIss = "https://gateway.example.test"
	testAud = "whatsapp-mcp"
)

var (
	keyOnce sync.Once
	keyA    *rsa.PrivateKey
	keyB    *rsa.PrivateKey
)

func testKeys(t *testing.T) (*rsa.PrivateKey, *rsa.PrivateKey) {
	t.Helper()
	keyOnce.Do(func() {
		var err error
		if keyA, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
		if keyB, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
	})
	return keyA, keyB
}

// jwksServer serves the given kid->key set and counts fetches. set may be
// swapped during a test to simulate key rotation.
type jwksServer struct {
	*httptest.Server
	hits atomic.Int32
	mu   sync.Mutex
	set  map[string]*rsa.PublicKey
}

func newJWKSServer(t *testing.T, set map[string]*rsa.PublicKey) *jwksServer {
	t.Helper()
	s := &jwksServer{set: set}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.hits.Add(1)
		s.mu.Lock()
		defer s.mu.Unlock()
		var keys []map[string]string
		for kid, k := range s.set {
			keys = append(keys, map[string]string{
				"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *jwksServer) swap(set map[string]*rsa.PublicKey) {
	s.mu.Lock()
	s.set = set
	s.mu.Unlock()
}

func validClaims() jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss":   testIss,
		"aud":   testAud,
		"sub":   "Alice@Example.com",
		"iat":   now.Unix(),
		"exp":   now.Add(5 * time.Minute).Unix(),
		"scope": "mcp:tools/call mcp:tools/whatsapp_send_text:call",
	}
}

func sign(t *testing.T, method jwt.SigningMethod, key any, kid string, c jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, c)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newTestVerifier(t *testing.T, url string) *Verifier {
	t.Helper()
	v, err := NewVerifier(NewJWKS(url), testIss, testAud, []string{"example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func with(mut func(jwt.MapClaims)) jwt.MapClaims {
	c := validClaims()
	mut(c)
	return c
}

func TestVerifyRejectsEveryBrokenToken(t *testing.T) {
	a, b := testKeys(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{"k1": &a.PublicKey})
	v := newTestVerifier(t, srv.URL)

	hsSecret := a.PublicKey.N.Bytes()
	cases := []struct {
		name  string
		token string
	}{
		{"wrong audience", sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["aud"] = "other" }))},
		{"wrong issuer", sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["iss"] = "https://evil.test" }))},
		{"issuer prefix only", sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["iss"] = testIss + ".evil" }))},
		{"expired", sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }))},
		{"no exp", sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { delete(c, "exp") }))},
		{"issued in the future", sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["iat"] = time.Now().Add(time.Hour).Unix() }))},
		{"HS256 with the public key as secret", sign(t, jwt.SigningMethodHS256, hsSecret, "k1", validClaims())},
		{"RS512 is not RS256", sign(t, jwt.SigningMethodRS512, a, "k1", validClaims())},
		{"alg none", sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, "k1", validClaims())},
		{"unknown kid", sign(t, jwt.SigningMethodRS256, a, "nope", validClaims())},
		{"no kid", sign(t, jwt.SigningMethodRS256, a, "", validClaims())},
		{"signed by another key under a known kid", sign(t, jwt.SigningMethodRS256, b, "k1", validClaims())},
		{"sub is not an email", sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["sub"] = "user-123" }))},
		{"sub with display name", sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["sub"] = "Alice <alice@example.com>" }))},
		{"domain not allowed", sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["sub"] = "alice@other.com" }))},
		{"subdomain is not the domain", sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["sub"] = "alice@sub.example.com" }))},
		{"lookalike domain", sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["sub"] = "alice@example.com.evil.test" }))},
		{"garbage", "not.a.jwt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if id, err := v.Verify(context.Background(), c.token); err == nil {
				t.Fatalf("token accepted as %+v", id)
			}
		})
	}

	id, err := v.Verify(context.Background(), sign(t, jwt.SigningMethodRS256, a, "k1", validClaims()))
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if id.Email != "alice@example.com" {
		t.Fatalf("owner email = %q, want it lowercased", id.Email)
	}
	if len(id.Scopes) != 2 || id.Expires.IsZero() {
		t.Fatalf("identity = %+v", id)
	}
}

func TestJWKSRefetchesOnceForAnUnknownKidAndPicksUpRotation(t *testing.T) {
	a, b := testKeys(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{"k1": &a.PublicKey})
	v := newTestVerifier(t, srv.URL)
	jwks := v.keys.(*JWKS)
	clock := time.Now()
	jwks.now = func() time.Time { return clock }

	if _, err := v.Verify(context.Background(), sign(t, jwt.SigningMethodRS256, a, "k1", validClaims())); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), sign(t, jwt.SigningMethodRS256, a, "k1", validClaims())); err != nil {
		t.Fatal(err)
	}
	if got := srv.hits.Load(); got != 1 {
		t.Fatalf("a cached kid must not refetch, got %d fetches", got)
	}

	for range 5 {
		_, _ = v.Verify(context.Background(), sign(t, jwt.SigningMethodRS256, a, "random", validClaims()))
	}
	if got := srv.hits.Load(); got != 1 {
		t.Fatalf("unknown kids right after a fetch must be rate-limited, got %d fetches", got)
	}

	srv.swap(map[string]*rsa.PublicKey{"k2": &b.PublicKey})
	clock = clock.Add(jwksMinRefetch)
	if _, err := v.Verify(context.Background(), sign(t, jwt.SigningMethodRS256, b, "k2", validClaims())); err != nil {
		t.Fatalf("rotated key must be picked up by one refetch: %v", err)
	}
	if got := srv.hits.Load(); got != 2 {
		t.Fatalf("want exactly one refetch for the new kid, got %d fetches", got)
	}
}

func TestNewVerifierRequiresConfig(t *testing.T) {
	keys := NewJWKS("http://unused")
	if _, err := NewVerifier(keys, "", testAud, []string{"example.com"}); err == nil {
		t.Error("empty issuer must be refused")
	}
	if _, err := NewVerifier(keys, testIss, "", []string{"example.com"}); err == nil {
		t.Error("empty audience must be refused")
	}
	if _, err := NewVerifier(keys, testIss, testAud, ParseDomains(" , ")); err == nil {
		t.Error("an empty domain list must be refused")
	}
}
