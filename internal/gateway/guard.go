package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// maxAnonymousBody caps what is read from a request that carries no token.
// Such bodies are read before any authentication, and the methods allowed
// without a user are all tiny.
const maxAnonymousBody = 1 << 20

// publicMethods may be served without a user JWT: the gateway itself calls
// them to build its catalog and check health, and none of them touches a
// WhatsApp account.
var publicMethods = map[string]bool{
	"initialize": true,
	"ping":       true,
	"tools/list": true,
}

func isPublicMethod(method string) bool {
	return publicMethods[method] || strings.HasPrefix(method, "notifications/")
}

// mcpGuard puts /mcp behind the gateway. Session housekeeping (GET, DELETE)
// and POST bodies made only of public methods need no user, but any token
// presented must still be a valid gateway token. Everything else, and any
// batch that mixes in another method, requires a user JWT with an allowed
// email, which the SDK middleware turns into the request's TokenInfo.
func mcpGuard(v *Verifier, next http.Handler) http.Handler {
	requireUser := auth.RequireBearerToken(v.TokenVerifier(), nil)(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, hasToken, ok := bearerToken(r)
		if !ok {
			http.Error(w, "malformed authorization header", http.StatusUnauthorized)
			return
		}
		if hasToken {
			if err := v.VerifyGateway(r.Context(), token); err != nil {
				log.Printf("gateway: rejected token: %v", err)
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
		}
		switch r.Method {
		case http.MethodGet, http.MethodDelete:
			next.ServeHTTP(w, r)
			return
		case http.MethodPost:
		default:
			requireUser.ServeHTTP(w, r)
			return
		}
		body, err := readBody(w, r, hasToken)
		if err != nil {
			http.Error(w, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if onlyPublicMethods(body) {
			next.ServeHTTP(w, r)
			return
		}
		requireUser.ServeHTTP(w, r)
	})
}

// bearerToken returns the bearer token, whether one was sent, and false if the
// Authorization header is present but not a bearer token.
func bearerToken(r *http.Request) (token string, present, ok bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false, true
	}
	fields := strings.Fields(h)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return "", false, false
	}
	return fields[1], true, true
}

func readBody(w http.ResponseWriter, r *http.Request, authenticated bool) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body := r.Body
	if !authenticated {
		body = http.MaxBytesReader(w, r.Body, maxAnonymousBody)
	}
	defer body.Close()
	return io.ReadAll(body)
}

// onlyPublicMethods reports whether every message in a JSON-RPC body (single
// or batch) is a request or notification for a public method. Messages are
// decoded with the SDK's own decoder so this cannot read a different method
// than the server will dispatch. Anything unparseable is not public.
func onlyPublicMethods(body []byte) bool {
	raws := []json.RawMessage{body}
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(trimmed, &batch); err != nil || len(batch) == 0 {
			return false
		}
		raws = batch
	}
	for _, raw := range raws {
		msg, err := jsonrpc.DecodeMessage(raw)
		if err != nil {
			return false
		}
		req, isReq := msg.(*jsonrpc.Request)
		if !isReq || !isPublicMethod(req.Method) {
			return false
		}
	}
	return true
}
