package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

const (
	testIssuer   = "https://mcp.example.com"
	testResource = "https://mcp.example.com/mcp"
	loopbackURI  = "http://127.0.0.1:33418/callback"
)

func newHardenedStore(t *testing.T) *Store {
	t.Helper()
	st := NewStore(testJWTSecret, testIssuer, testResource, newTestDB(t), func(string) bool { return true })
	if err := st.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

// fakePairer records started and ended flows instead of reaching WhatsApp.
type fakePairer struct {
	mu      sync.Mutex
	started map[string]bool
	ended   map[string]bool
}

func newFakePairer() *fakePairer {
	return &fakePairer{started: map[string]bool{}, ended: map[string]bool{}}
}

func (p *fakePairer) StartFlow(id string) (*wa.PairFlow, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started[id] = true
	return &wa.PairFlow{}, nil
}

func (p *fakePairer) Flow(string) (*wa.PairFlow, bool) { return nil, false }

func (p *fakePairer) EndFlow(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ended[id] = true
}

func (p *fakePairer) counts() (started, ended int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.started), len(p.ended)
}

func serve(h http.Handler, method, target, ip string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = ip + ":40000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestValidateRedirectURI(t *testing.T) {
	hosts := DefaultRedirectHosts
	cases := []struct {
		uri string
		ok  bool
	}{
		{"http://127.0.0.1:33418/callback", true},
		{"http://localhost:8080/cb", true},
		{"http://[::1]:9000/cb", true},
		{"https://claude.ai/api/mcp/auth_callback", true},
		{"https://claude.com/api/mcp/auth_callback", true},
		{"http://127.0.0.1/callback", false},
		{"http://evil.example/cb", false},
		{"http://localhost.evil.example:80/cb", false},
		{"https://evil.example/cb", false},
		{"https://claude.ai.evil.example/cb", false},
		{"https://claude.ai/cb#frag", false},
		{"https://user@claude.ai/cb", false},
		{"http://user:pw@127.0.0.1:80/cb", false},
		{"javascript:alert(1)", false},
		{"custom-scheme://cb", false},
		{"ftp://127.0.0.1:21/cb", false},
		{"", false},
		{"/relative/cb", false},
	}
	for _, c := range cases {
		if err := validateRedirectURI(c.uri, hosts); (err == nil) != c.ok {
			t.Errorf("validateRedirectURI(%q) = %v, want ok=%v", c.uri, err, c.ok)
		}
	}
	if err := validateRedirectURIs(nil, hosts); err == nil {
		t.Error("an empty redirect_uris list must be rejected")
	}
	if err := validateRedirectURI("https://my.example/cb", []string{"my.example"}); err != nil {
		t.Errorf("a configured host must be accepted: %v", err)
	}
}

func TestRegisterValidatesAndIsRateLimited(t *testing.T) {
	st := newHardenedStore(t)
	mux := http.NewServeMux()
	NewHandlers(st, newFakePairer(), testIssuer, Config{}).Register(mux, http.NotFoundHandler())

	for _, body := range []string{
		`{"redirect_uris":[]}`,
		`{"client_name":"no uris"}`,
		`{"redirect_uris":["https://evil.example/cb"]}`,
		`{"redirect_uris":["` + loopbackURI + `","https://evil.example/cb"]}`,
		`not json`,
	} {
		if rec := serve(mux, http.MethodPost, "/register", "10.0.0.1", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("register %s = %d, want 400", body, rec.Code)
		}
	}

	ok := `{"redirect_uris":["` + loopbackURI + `"]}`
	allowed := 0
	for range 20 {
		if serve(mux, http.MethodPost, "/register", "10.0.0.2", ok).Code == http.StatusCreated {
			allowed++
		}
	}
	if allowed == 0 || allowed >= 20 {
		t.Fatalf("one address registered %d of 20 clients in a burst, want a rate limit", allowed)
	}
	if rec := serve(mux, http.MethodPost, "/register", "10.0.0.3", ok); rec.Code != http.StatusCreated {
		t.Fatalf("another address must not share the budget, got %d", rec.Code)
	}
}

func TestRegisteredClientsAreCapped(t *testing.T) {
	st := newHardenedStore(t)
	st.maxClients = 3
	var ids []string
	for range 5 {
		ids = append(ids, st.RegisterClient([]string{loopbackURI}, "c").ID)
	}
	for i, id := range ids {
		_, ok := st.Client(id)
		if want := i >= 2; ok != want {
			t.Fatalf("client %d present = %v, want %v (the oldest must be evicted)", i, ok, want)
		}
	}
}

func authorizeURL(clientID, redirect, resource string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirect},
		"code_challenge":        {"x"},
		"code_challenge_method": {"S256"},
	}
	if resource != "" {
		q.Set("resource", resource)
	}
	return "/authorize?" + q.Encode()
}

func TestAuthorizeRejectsUnregisteredRedirectsAndForeignResources(t *testing.T) {
	st := newHardenedStore(t)
	pairer := newFakePairer()
	mux := http.NewServeMux()
	NewHandlers(st, pairer, testIssuer, Config{}).Register(mux, http.NotFoundHandler())
	bare := st.RegisterClient(nil, "registered without uris")
	good := st.RegisterClient([]string{loopbackURI}, "ok")

	cases := []struct {
		name string
		url  string
		want int
	}{
		{"client without registered uris", authorizeURL(bare.ID, loopbackURI, ""), http.StatusBadRequest},
		{"uri not registered", authorizeURL(good.ID, "http://127.0.0.1:1/other", ""), http.StatusBadRequest},
		{"foreign resource", authorizeURL(good.ID, loopbackURI, "https://other.example/mcp"), http.StatusBadRequest},
		{"own resource", authorizeURL(good.ID, loopbackURI, testResource), http.StatusFound},
		{"own resource with trailing slash", authorizeURL(good.ID, loopbackURI, testResource+"/"), http.StatusFound},
		{"no resource", authorizeURL(good.ID, loopbackURI, ""), http.StatusFound},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ip := "10.1.0." + string(rune('1'+i))
			if rec := serve(mux, http.MethodGet, c.url, ip, ""); rec.Code != c.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

func TestPairingFlowsAreCappedAndExpire(t *testing.T) {
	st := newHardenedStore(t)
	pairer := newFakePairer()
	mux := http.NewServeMux()
	h := NewHandlers(st, pairer, testIssuer, Config{MaxFlows: 2, MaxFlowsPerIP: 1, FlowTTL: 100 * time.Millisecond})
	h.Register(mux, http.NotFoundHandler())
	c := st.RegisterClient([]string{loopbackURI}, "ok")
	auth := func(ip string) int {
		return serve(mux, http.MethodGet, authorizeURL(c.ID, loopbackURI, ""), ip, "").Code
	}

	if got := auth("10.2.0.1"); got != http.StatusFound {
		t.Fatalf("first flow = %d", got)
	}
	if got := auth("10.2.0.1"); got != http.StatusTooManyRequests {
		t.Fatalf("second flow from one address = %d, want 429", got)
	}
	if got := auth("10.2.0.2"); got != http.StatusFound {
		t.Fatalf("flow from another address = %d", got)
	}
	if got := auth("10.2.0.3"); got != http.StatusTooManyRequests {
		t.Fatalf("flow past the global cap = %d, want 429", got)
	}

	deadline := time.Now().Add(5 * time.Second)
	for h.activeFlows() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("abandoned flows never expired")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if started, ended := pairer.counts(); started != 2 || ended != 2 {
		t.Fatalf("started %d flows and tore down %d, want every abandoned flow torn down", started, ended)
	}
	if got := auth("10.2.0.3"); got != http.StatusFound {
		t.Fatalf("after expiry a new flow must start, got %d", got)
	}
}

func TestAuthorizeAndLoginAreRateLimitedPerIP(t *testing.T) {
	st := newHardenedStore(t)
	mux := http.NewServeMux()
	NewHandlers(st, newFakePairer(), testIssuer, Config{MaxFlows: 1000, MaxFlowsPerIP: 1000}).Register(mux, http.NotFoundHandler())
	c := st.RegisterClient([]string{loopbackURI}, "ok")
	for _, target := range []string{authorizeURL(c.ID, loopbackURI, ""), "/login/pairing?authFlow=x", "/login/status?authFlow=x"} {
		limited := false
		for range 200 {
			method := http.MethodGet
			if strings.HasPrefix(target, "/login/pairing") {
				method = http.MethodPost
			}
			if serve(mux, method, target, "10.3.0.1", "").Code == http.StatusTooManyRequests {
				limited = true
				break
			}
		}
		if !limited {
			t.Errorf("%s was never rate limited for one address", target)
		}
	}
}

func signWith(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBearerGuardChecksAudience(t *testing.T) {
	st := newHardenedStore(t)
	mux := http.NewServeMux()
	NewHandlers(st, nil, testIssuer, Config{}).Register(mux, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	now := time.Now()
	base := func(mut func(jwt.MapClaims)) string {
		c := jwt.MapClaims{"iss": testIssuer, "aud": testResource, "sub": "5511999999999",
			"iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
		mut(c)
		return signWith(t, c)
	}
	own, err := st.Issue(context.Background(), "c", "5511999999999", "https://other.example/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		token string
		want  int
	}{
		{"issued here, even when a foreign resource was stored", own.AccessToken, http.StatusOK},
		{"own audience", base(func(jwt.MapClaims) {}), http.StatusOK},
		{"another resource", base(func(c jwt.MapClaims) { c["aud"] = "https://other.example/mcp" }), http.StatusUnauthorized},
		{"no audience", base(func(c jwt.MapClaims) { delete(c, "aud") }), http.StatusUnauthorized},
		{"no expiry", base(func(c jwt.MapClaims) { delete(c, "exp") }), http.StatusUnauthorized},
		{"another issuer", base(func(c jwt.MapClaims) { c["iss"] = "https://other.example" }), http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", "Bearer "+c.token)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
		})
	}
}

func TestTokenExchangeChecksRedirectURI(t *testing.T) {
	st := newHardenedStore(t)
	mux := http.NewServeMux()
	NewHandlers(st, nil, testIssuer, Config{}).Register(mux, http.NotFoundHandler())
	verifier := "verifier-0123456789-0123456789-0123456789"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	exchange := func(redirect string) int {
		code := st.IssueCode(&Pending{ClientID: "c", RedirectURI: loopbackURI, CodeChallenge: challenge, Resource: testResource}, "5511999999999")
		form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirect}}
		req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := exchange("http://127.0.0.1:1/other"); got != http.StatusBadRequest {
		t.Fatalf("a code redeemed with another redirect_uri = %d, want 400", got)
	}
	if got := exchange(loopbackURI); got != http.StatusOK {
		t.Fatalf("matching redirect_uri = %d, want 200", got)
	}
}

func TestRevokeSubDropsOnlyThatAccountsRefreshTokens(t *testing.T) {
	ctx := context.Background()
	st := newHardenedStore(t)
	gone, err := st.Issue(ctx, "c", "5511111111111", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := st.Issue(ctx, "c", "5522222222222", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeSub(ctx, "5511111111111"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RotateRefresh(ctx, gone.RefreshToken); err == nil {
		t.Fatal("a revoked account's refresh token still works")
	}
	if _, err := st.RotateRefresh(ctx, kept.RefreshToken); err != nil {
		t.Fatalf("another account's refresh token was revoked: %v", err)
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name string
		hops int
		xff  []string
		want string
	}{
		{"no trust ignores the header", 0, []string{"100.64.0.9"}, "127.0.0.1"},
		{"one hop takes the rightmost entry", 1, []string{"6.6.6.6, 100.64.0.9"}, "100.64.0.9"},
		{"entries across repeated headers", 1, []string{"6.6.6.6", "100.64.0.9"}, "100.64.0.9"},
		{"two hops skip the inner proxy", 2, []string{"6.6.6.6, 100.64.0.9, 10.0.0.2"}, "100.64.0.9"},
		{"missing header falls back to the peer", 1, nil, "127.0.0.1"},
		{"too few entries fall back to the peer", 2, []string{"100.64.0.9"}, "127.0.0.1"},
		{"garbage falls back to the peer", 1, []string{"not-an-ip"}, "127.0.0.1"},
		{"ipv6 entry", 1, []string{"fd7a:115c:a1e0::1"}, "fd7a:115c:a1e0::1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "127.0.0.1:50000"
			for _, v := range c.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			if got := clientIP(req, c.hops); got != c.want {
				t.Fatalf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

// Behind tailscale serve every request arrives from the proxy's loopback
// address. With hops = 1 two tailnet clients get their own budgets; with 0 a
// client cannot dodge its budget by inventing X-Forwarded-For values.
func TestRateLimitsBehindAProxy(t *testing.T) {
	ok := `{"redirect_uris":["` + loopbackURI + `"]}`
	register := func(h http.Handler, xff string) int {
		req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(ok))
		req.RemoteAddr = "127.0.0.1:50000"
		req.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	newMux := func(hops int) http.Handler {
		mux := http.NewServeMux()
		NewHandlers(newHardenedStore(t), newFakePairer(), testIssuer, Config{TrustProxyHops: hops}).Register(mux, http.NotFoundHandler())
		return mux
	}

	proxied := newMux(1)
	for i := 0; register(proxied, "100.64.0.1") != http.StatusTooManyRequests; i++ {
		if i > 50 {
			t.Fatal("one client behind the proxy was never rate limited")
		}
	}
	if got := register(proxied, "100.64.0.2"); got != http.StatusCreated {
		t.Fatalf("a second client behind the proxy shares the first one's budget: %d", got)
	}

	direct := newMux(0)
	limited := false
	for i := range 50 {
		if register(direct, "100.64.1."+strconv.Itoa(i)) == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("with no trusted proxy, varying X-Forwarded-For escaped the rate limit")
	}
}
