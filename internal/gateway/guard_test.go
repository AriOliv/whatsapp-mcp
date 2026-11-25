package gateway

import (
	"crypto/rsa"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	listReq   = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	initReq   = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"gw","version":"1"}}}`
	pingReq   = `{"jsonrpc":"2.0","id":2,"method":"ping"}`
	initedNtf = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	callReq   = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whatsapp_find_chats","arguments":{}}}`
	readReq   = `{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"ui://x"}}`
	response  = `{"jsonrpc":"2.0","id":9,"result":{}}`
)

// TestGuardSplitsPublicMethodsFromToolCalls is the contract the gateway relies
// on: catalog and health traffic needs no user, a tool call always does.
func TestGuardSplitsPublicMethodsFromToolCalls(t *testing.T) {
	a, _ := testKeys(t)
	jwks := newJWKSServer(t, map[string]*rsa.PublicKey{"k1": &a.PublicKey})
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := "-"
		if ti := auth.TokenInfoFromContext(r.Context()); ti != nil {
			user = ti.UserID
		}
		body, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, user+"|"+string(body))
	})
	srv := httptest.NewServer(NewMux(newTestVerifier(t, jwks.URL), echo))
	defer srv.Close()

	user := sign(t, jwt.SigningMethodRS256, a, "k1", validClaims())
	proxy := sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["sub"] = "litellm-proxy" }))
	foreign := sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["aud"] = "someone-else" }))
	cases := []struct {
		name     string
		method   string
		token    string
		body     string
		wantCode int
		wantUser string
	}{
		{"initialize without a token", "POST", "", initReq, 200, "-"},
		{"tools/list without a token", "POST", "", listReq, 200, "-"},
		{"ping without a token", "POST", "", pingReq, 200, "-"},
		{"notification without a token", "POST", "", initedNtf, 200, "-"},
		{"batch of public methods without a token", "POST", "", "[" + listReq + "," + initedNtf + "]", 200, "-"},
		{"tools/list with a non-user gateway token", "POST", proxy, listReq, 200, "-"},
		{"tools/list with a user token", "POST", user, listReq, 200, "-"},
		{"tools/list with a token for another audience", "POST", foreign, listReq, 401, ""},
		{"tools/list with a malformed authorization header", "POST", "Basic abc", listReq, 401, ""},
		{"tools/call without a token", "POST", "", callReq, 401, ""},
		{"tools/call with a non-user gateway token", "POST", proxy, callReq, 401, ""},
		{"tools/call with a user token", "POST", user, callReq, 200, "alice@example.com"},
		{"batch mixing list and call without a token", "POST", "", "[" + listReq + "," + callReq + "]", 401, ""},
		{"batch mixing list and call with a non-user token", "POST", proxy, "[" + listReq + "," + callReq + "]", 401, ""},
		{"batch mixing list and call with a user token", "POST", user, "[" + listReq + "," + callReq + "]", 200, "alice@example.com"},
		{"other methods need a user", "POST", "", readReq, 401, ""},
		{"a client response needs a user", "POST", "", response, 401, ""},
		{"unparseable body needs a user", "POST", "", `{"method":`, 401, ""},
		{"empty batch needs a user", "POST", "", `[]`, 401, ""},
		{"session stream without a token", "GET", "", "", 200, "-"},
		{"session delete without a token", "DELETE", "", "", 200, "-"},
		{"session stream with a token for another audience", "GET", foreign, "", 401, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest(c.method, srv.URL+"/mcp", strings.NewReader(c.body))
			switch {
			case strings.HasPrefix(c.token, "Basic "):
				req.Header.Set("Authorization", c.token)
			case c.token != "":
				req.Header.Set("Authorization", "Bearer "+c.token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != c.wantCode {
				t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, c.wantCode, got)
			}
			if c.wantCode != 200 {
				return
			}
			if u, body, _ := strings.Cut(string(got), "|"); u != c.wantUser || body != c.body {
				t.Fatalf("handler saw user %q body %q, want %q and the body untouched", u, body, c.wantUser)
			}
		})
	}
}

func TestGuardCapsAnonymousBodies(t *testing.T) {
	a, _ := testKeys(t)
	jwks := newJWKSServer(t, map[string]*rsa.PublicKey{"k1": &a.PublicKey})
	srv := httptest.NewServer(NewMux(newTestVerifier(t, jwks.URL), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	defer srv.Close()
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"pad":"` + strings.Repeat("x", maxAnonymousBody) + `"}}`
	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized anonymous body got %d", resp.StatusCode)
	}
}
