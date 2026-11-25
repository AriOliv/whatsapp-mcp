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

func TestGatewayRouteSurface(t *testing.T) {
	a, _ := testKeys(t)
	jwks := newJWKSServer(t, map[string]*rsa.PublicKey{"k1": &a.PublicKey})
	echoUser := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, auth.TokenInfoFromContext(r.Context()).UserID)
	})
	srv := httptest.NewServer(NewMux(newTestVerifier(t, jwks.URL), echoUser))
	defer srv.Close()
	valid := sign(t, jwt.SigningMethodRS256, a, "k1", validClaims())
	wrongAud := sign(t, jwt.SigningMethodRS256, a, "k1", with(func(c jwt.MapClaims) { c["aud"] = "x" }))

	cases := []struct {
		name     string
		method   string
		path     string
		token    string
		wantCode int
		wantBody string
	}{
		{"healthz reveals nothing", http.MethodGet, "/healthz", "", 200, `{"ok":true}`},
		{"mcp without token", http.MethodPost, "/mcp", "", 401, ""},
		{"mcp with a rejected token", http.MethodPost, "/mcp", wrongAud, 401, ""},
		{"mcp with a valid token", http.MethodPost, "/mcp", valid, 200, "alice@example.com"},
		{"no dynamic client registration", http.MethodPost, "/register", "", 404, ""},
		{"no oauth authorize", http.MethodGet, "/authorize", "", 404, ""},
		{"no oauth token", http.MethodPost, "/token", "", 404, ""},
		{"no oauth metadata", http.MethodGet, "/.well-known/oauth-authorization-server", "", 404, ""},
		{"no login page", http.MethodGet, "/login", "", 404, ""},
		{"no login polling", http.MethodGet, "/login/status", "", 404, ""},
		{"no gateway api", http.MethodPost, "/api/send/text", valid, 404, ""},
		{"no web forms", http.MethodGet, "/f/abc", "", 404, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest(c.method, srv.URL+c.path, strings.NewReader("{}"))
			if c.token != "" {
				req.Header.Set("Authorization", "Bearer "+c.token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != c.wantCode {
				t.Fatalf("%s %s = %d, want %d", c.method, c.path, resp.StatusCode, c.wantCode)
			}
			if c.wantBody != "" && strings.TrimSpace(string(body)) != c.wantBody {
				t.Fatalf("body = %q, want %q", body, c.wantBody)
			}
		})
	}
}
