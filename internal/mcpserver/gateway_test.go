package mcpserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AriOliv/whatsapp-mcp/internal/authctx"
	"github.com/AriOliv/whatsapp-mcp/internal/gateway"
	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

const (
	alice  = "alice@example.com"
	bob    = "bob@example.com"
	carol  = "carol@example.com"
	phoneA = "5511111111111"
	phoneB = "5522222222222"
)

type mapAccounts map[string]string

func (m mapAccounts) AccountOf(_ context.Context, owner string) (string, error) { return m[owner], nil }

func toolCall(owner, tool string) *mcp.CallToolRequest {
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tool}}
	if owner != "" {
		req.Extra = &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: owner}}
	}
	return req
}

func callResultText(t *testing.T, res mcp.Result) string {
	t.Helper()
	r, ok := res.(*mcp.CallToolResult)
	if !ok || len(r.Content) == 0 {
		return ""
	}
	return r.Content[0].(*mcp.TextContent).Text
}

func TestResolveOwnerBindsEachCallToItsOwnersNumber(t *testing.T) {
	accounts := mapAccounts{alice: phoneA, bob: phoneB}
	cases := []struct {
		name        string
		owner       string
		tool        string
		inherited   string
		wantAccount string
		wantCalled  bool
		wantErr     string
	}{
		{"alice acts on her number", alice, "whatsapp_send_text", "", phoneA, true, ""},
		{"bob acts on his number", bob, "whatsapp_find_chats", "", phoneB, true, ""},
		{"a session cannot carry another number", alice, "whatsapp_send_text", phoneB, phoneA, true, ""},
		{"unbound owner is told to connect", carol, "whatsapp_send_text", "", "", false, "whatsapp_connect"},
		{"unbound owner cannot reach an inherited number", carol, "whatsapp_find_chats", phoneB, "", false, "whatsapp_connect"},
		{"unbound owner may connect", carol, "whatsapp_connect", phoneB, "", true, ""},
		{"no verified owner", "", "whatsapp_connection_status", "", "", false, "unauthenticated"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			called := false
			var seenAccount, seenOwner string
			next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
				called = true
				seenAccount, seenOwner = authctx.Account(ctx), authctx.Owner(ctx)
				return &mcp.CallToolResult{}, nil
			}
			ctx := authctx.WithAccount(context.Background(), c.inherited)
			res, err := resolveOwner(accounts)(next)(ctx, "tools/call", toolCall(c.owner, c.tool))
			if err != nil {
				t.Fatal(err)
			}
			if called != c.wantCalled {
				t.Fatalf("handler called = %v, want %v (result %q)", called, c.wantCalled, callResultText(t, res))
			}
			if called && (seenAccount != c.wantAccount || seenOwner != c.owner) {
				t.Fatalf("handler saw account %q owner %q, want %q %q", seenAccount, seenOwner, c.wantAccount, c.owner)
			}
			if c.wantErr != "" && !strings.Contains(callResultText(t, res), c.wantErr) {
				t.Fatalf("result %q should mention %q", callResultText(t, res), c.wantErr)
			}
		})
	}
}

func TestSendLimiterIsPerAccountAndOnlyForSends(t *testing.T) {
	l := NewSendLimiter(2, 2)
	clock := time.Now()
	l.now = func() time.Time { return clock }
	calls := 0
	next := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		calls++
		return &mcp.CallToolResult{}, nil
	}
	h := limitSends(l)(next)
	send := func(account, tool string) string {
		ctx := authctx.WithAccount(context.Background(), account)
		res, _ := h(ctx, "tools/call", toolCall(alice, tool))
		return callResultText(t, res)
	}

	for i := range 2 {
		if msg := send(phoneA, "whatsapp_send_text"); msg != "" {
			t.Fatalf("send %d within the burst was refused: %s", i+1, msg)
		}
	}
	if msg := send(phoneA, "whatsapp_send_media"); !strings.Contains(msg, "rate limit") {
		t.Fatalf("third send must be refused with a clear message, got %q", msg)
	}
	if msg := send(phoneB, "whatsapp_send_text"); msg != "" {
		t.Fatalf("another account has its own budget, got %q", msg)
	}
	if msg := send(phoneA, "whatsapp_find_chats"); msg != "" {
		t.Fatalf("reads are not rate limited, got %q", msg)
	}
	clock = clock.Add(30 * time.Second)
	if msg := send(phoneA, "whatsapp_send_text"); msg != "" {
		t.Fatalf("the budget must refill over time, got %q", msg)
	}
	if calls != 5 {
		t.Fatalf("handler ran %d times, want 5", calls)
	}
}

// TestGatewayEndToEnd drives the real HTTP stack: a gateway-signed JWT, the
// bearer check, the MCP session, owner resolution and a strict manager.
func TestGatewayEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(jwks.Close)
	verifier, err := gateway.NewVerifier(gateway.NewJWKS(jwks.URL), "https://gw.test", "wa", []string{"example.com"})
	if err != nil {
		t.Fatal(err)
	}

	dsn := "file:" + filepath.Join(t.TempDir(), "e2e.db") + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	st, db, err := appstore.Open(dsn, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitOwners(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Bind(ctx, alice, phoneA); err != nil {
		t.Fatal(err)
	}
	if err := st.Bind(ctx, bob, phoneB); err != nil {
		t.Fatal(err)
	}
	mgr, err := wa.New(ctx, db, false, st, "", wa.WithStrictAccounts())
	if err != nil {
		t.Fatal(err)
	}
	srv := BuildGateway(mgr, Gateway{Accounts: st, Pairing: gateway.NewPairing(st, mgr), Limiter: NewSendLimiter(20, 5)})
	httpSrv := httptest.NewServer(gateway.NewMux(verifier, mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv }, nil)))
	t.Cleanup(httpSrv.Close)

	session := func(owner string) *mcp.ClientSession {
		if owner == "" {
			client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
			cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpSrv.URL + "/mcp"}, nil)
			if err != nil {
				t.Fatalf("connect without a token: %v", err)
			}
			t.Cleanup(func() { _ = cs.Close() })
			return cs
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": "https://gw.test", "aud": "wa", "sub": owner,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
		})
		tok.Header["kid"] = "k1"
		signed, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
		cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   httpSrv.URL + "/mcp",
			HTTPClient: &http.Client{Transport: bearer{token: signed}},
		}, nil)
		if err != nil {
			t.Fatalf("connect as %s: %v", owner, err)
		}
		t.Cleanup(func() { _ = cs.Close() })
		return cs
	}
	callText := func(cs *mcp.ClientSession, tool string, args map[string]any) string {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return res.Content[0].(*mcp.TextContent).Text
	}

	aliceSession := session(alice)
	got := callText(aliceSession, "whatsapp_instance_state", nil)
	if !strings.Contains(got, phoneA) || strings.Contains(got, phoneB) {
		t.Fatalf("alice's call must act on her number only, got %q", got)
	}
	got = callText(aliceSession, "whatsapp_send_text", map[string]any{"number": phoneB, "text": "hi"})
	if !strings.Contains(got, phoneA) {
		t.Fatalf("naming bob's number as recipient must still send from alice's account, got %q", got)
	}

	carolSession := session(carol)
	if got := callText(carolSession, "whatsapp_find_chats", nil); !strings.Contains(got, "whatsapp_connect") {
		t.Fatalf("an unbound owner must be told to connect, got %q", got)
	}
	if got := callText(carolSession, "whatsapp_connect", map[string]any{"phone_number": phoneA}); !strings.Contains(got, "already connected by another user") {
		t.Fatalf("connecting a number bound to someone else must be refused, got %q", got)
	}

	tools, err := carolSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"whatsapp_connect", "whatsapp_connection_status", "whatsapp_disconnect"} {
		if !names[want] {
			t.Errorf("tool %s missing in gateway mode", want)
		}
	}
	for _, hidden := range gatewayHiddenTools {
		if names[hidden] {
			t.Errorf("tool %s must be hidden in gateway mode", hidden)
		}
	}

	// The gateway builds its catalog without a user: no token at all, or its
	// own token whose sub is not an email. Listing must work, calling must not.
	for _, caller := range []string{"", "litellm-proxy"} {
		cs := session(caller)
		listed, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("listing tools as %q must work: %v", caller, err)
		}
		if len(listed.Tools) != len(tools.Tools) {
			t.Fatalf("catalog as %q has %d tools, a user sees %d", caller, len(listed.Tools), len(tools.Tools))
		}
		if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "whatsapp_connection_status"}); err == nil {
			t.Fatalf("a tool call as %q must be refused", caller)
		}
	}
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}
