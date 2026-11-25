package gateway

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

const (
	jwksFetchTimeout = 5 * time.Second
	jwksMaxBytes     = 1 << 20
	jwksTTL          = 10 * time.Minute
	// jwksMinRefetch rate-limits refetches triggered by an unknown kid. The kid
	// comes from an unverified header, so without this any caller could make
	// the server hammer the gateway's JWKS endpoint.
	jwksMinRefetch = 30 * time.Second
	minRSABits     = 2048
)

var errUnknownKID = errors.New("unknown signing key")

// JWKS fetches and caches the gateway's RS256 public keys.
type JWKS struct {
	url    string
	client *http.Client
	now    func() time.Time

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
}

// NewJWKS returns a key source for the JWKS document at url.
func NewJWKS(url string) *JWKS {
	return &JWKS{url: url, client: &http.Client{Timeout: jwksFetchTimeout}, now: time.Now}
}

// Key returns the RSA key for kid. A stale cache, or a kid it does not know,
// triggers one refetch (rate-limited); a failed refetch keeps serving the
// previous keys.
func (j *JWKS) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()
	key, known := j.keys[kid]
	fresh := j.keys != nil && now.Sub(j.fetchedAt) < jwksTTL
	if known && fresh {
		return key, nil
	}
	if j.keys == nil || now.Sub(j.lastAttempt) >= jwksMinRefetch {
		j.lastAttempt = now
		keys, err := j.fetch(ctx)
		if err != nil && j.keys == nil {
			return nil, err
		}
		if err == nil {
			j.keys, j.fetchedAt = keys, now
		}
	}
	if key, ok := j.keys[kid]; ok {
		return key, nil
	}
	return nil, errUnknownKID
}

type jwkDoc struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (j *JWKS) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	ctx, cancel := context.WithTimeout(ctx, jwksFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, nil)
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := j.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks: unexpected status %d", resp.StatusCode)
	}
	var doc jwkDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, jwksMaxBytes)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("jwks: decode: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}
		pub, err := rsaKey(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return nil, errors.New("jwks: no usable RS256 keys")
	}
	return keys, nil
}

func rsaKey(n64, e64 string) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(n64)
	if err != nil {
		return nil, err
	}
	eb, err := base64.RawURLEncoding.DecodeString(e64)
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nb)
	e := new(big.Int).SetBytes(eb)
	if n.BitLen() < minRSABits || !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31-1 {
		return nil, errors.New("jwks: weak or malformed RSA key")
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}
