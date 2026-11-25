package gateway

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// NewMux is the whole HTTP surface in gateway mode: /mcp behind the JWT check
// and a /healthz that reveals nothing about the accounts it serves.
func NewMux(v *Verifier, mcpHandler http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/mcp", auth.RequireBearerToken(v.TokenVerifier(), nil)(mcpHandler))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	return mux
}
