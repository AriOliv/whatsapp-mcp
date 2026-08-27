package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

// Handlers serves the OAuth 2.1 authorization-server endpoints, the WhatsApp
// pairing login page, and the bearer guard for /mcp.
type Handlers struct {
	store     *Store
	mgr       *wa.Manager
	publicURL string
	tmpl      *template.Template
}

func NewHandlers(store *Store, mgr *wa.Manager, publicURL string) *Handlers {
	return &Handlers{
		store:     store,
		mgr:       mgr,
		publicURL: strings.TrimRight(publicURL, "/"),
		tmpl:      template.Must(template.New("login").Parse(loginHTML)),
	}
}

// Register wires all routes onto mux; mcpHandler is the streamable-HTTP MCP
// handler, wrapped here with the bearer guard.
func (h *Handlers) Register(mux *http.ServeMux, mcpHandler http.Handler) {
	mux.HandleFunc("/.well-known/oauth-protected-resource", h.protectedResource)
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", h.protectedResource)
	mux.HandleFunc("/.well-known/oauth-authorization-server", h.authServerMeta)
	mux.HandleFunc("/authorize", h.authorize)
	mux.HandleFunc("/token", h.token)
	mux.HandleFunc("/register", h.register)
	mux.HandleFunc("/login", h.login)
	mux.HandleFunc("/login/qr", h.loginQR)
	mux.HandleFunc("/login/pairing", h.loginPairing)
	mux.HandleFunc("/login/status", h.loginStatus)
	mux.Handle("/mcp", h.BearerGuard(mcpHandler))
}

func (h *Handlers) protectedResource(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 h.publicURL + "/mcp",
		"authorization_servers":    []string{h.publicURL},
		"resource_name":            "WhatsApp MCP",
		"bearer_methods_supported": []string{"header"},
	})
}

func (h *Handlers) authServerMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                h.publicURL,
		"authorization_endpoint":                h.publicURL + "/authorize",
		"token_endpoint":                        h.publicURL + "/token",
		"registration_endpoint":                 h.publicURL + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"mcp:tools"},
	})
}

func (h *Handlers) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	if q.Get("response_type") != "code" {
		http.Error(w, "response_type must be code", http.StatusBadRequest)
		return
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "PKCE S256 required", http.StatusBadRequest)
		return
	}
	c, ok := h.store.Client(clientID)
	if !ok {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	if !allowedRedirect(c, redirectURI) {
		http.Error(w, "redirect_uri not registered", http.StatusBadRequest)
		return
	}
	p := &Pending{
		ClientID:      clientID,
		RedirectURI:   redirectURI,
		State:         q.Get("state"),
		Scopes:        strings.Fields(q.Get("scope")),
		CodeChallenge: q.Get("code_challenge"),
		Resource:      q.Get("resource"),
	}
	flowID := h.store.NewPending(p)
	if _, err := h.mgr.StartFlow(flowID); err != nil {
		h.store.DeletePending(flowID)
		http.Error(w, "could not start pairing: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/login?authFlow="+url.QueryEscape(flowID), http.StatusFound)
}

func (h *Handlers) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenErr(w, "invalid_request")
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		ac, ok := h.store.TakeCode(r.Form.Get("code"))
		if !ok {
			tokenErr(w, "invalid_grant")
			return
		}
		if !VerifyPKCE(r.Form.Get("code_verifier"), ac.CodeChallenge) {
			tokenErr(w, "invalid_grant")
			return
		}
		if cid := r.Form.Get("client_id"); cid != "" && cid != ac.ClientID {
			tokenErr(w, "invalid_client")
			return
		}
		issueAndWrite(r.Context(), w, h.store, ac.ClientID, ac.Sub, ac.Resource, ac.Scopes)
	case "refresh_token":
		rec, ok := h.store.TakeRefresh(r.Context(), r.Form.Get("refresh_token"))
		if !ok {
			tokenErr(w, "invalid_grant")
			return
		}
		issueAndWrite(r.Context(), w, h.store, rec.ClientID, rec.Sub, rec.Resource, rec.Scopes)
	default:
		tokenErr(w, "unsupported_grant_type")
	}
}

func (h *Handlers) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	c := h.store.RegisterClient(body.RedirectURIs, body.ClientName)
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  c.ID,
		"redirect_uris":              c.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	flowID := r.URL.Query().Get("authFlow")
	if _, ok := h.store.Pending(flowID); !ok {
		http.Error(w, "pairing flow expired — restart the connection", http.StatusBadRequest)
		return
	}
	w.Header().Set("content-type", "text/html; charset=utf-8")
	_ = h.tmpl.Execute(w, map[string]string{"FlowID": flowID})
}

func (h *Handlers) loginQR(w http.ResponseWriter, r *http.Request) {
	f, ok := h.mgr.Flow(r.URL.Query().Get("authFlow"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"state": "expired"})
		return
	}
	qr, pair, state, _, _ := f.Snapshot()
	b64 := ""
	if qr != "" {
		if png, err := qrcode.Encode(qr, qrcode.Medium, 256); err == nil {
			b64 = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"base64": b64, "pairingCode": pair, "state": state})
}

func (h *Handlers) loginPairing(w http.ResponseWriter, r *http.Request) {
	f, ok := h.mgr.Flow(r.URL.Query().Get("authFlow"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "expired"})
		return
	}
	var body struct {
		Number string `json:"number"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	code, err := f.RequestPhoneCode(r.Context(), body.Number)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pairingCode": code})
}

func (h *Handlers) loginStatus(w http.ResponseWriter, r *http.Request) {
	flowID := r.URL.Query().Get("authFlow")
	p, okP := h.store.Pending(flowID)
	f, okF := h.mgr.Flow(flowID)
	if !okP || !okF {
		writeJSON(w, http.StatusOK, map[string]any{"state": "expired"})
		return
	}
	_, _, state, sub, errMsg := f.Snapshot()
	if state == "open" && sub != "" {
		code := h.store.IssueCode(p, sub)
		redir := p.RedirectURI + "?code=" + url.QueryEscape(code)
		if p.State != "" {
			redir += "&state=" + url.QueryEscape(p.State)
		}
		h.store.DeletePending(flowID)
		h.mgr.EndFlow(flowID)
		writeJSON(w, http.StatusOK, map[string]any{"state": "open", "redirect": redir})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": state, "error": errMsg})
}

// BearerGuard verifies the JWT and injects the subject (phone) into the context.
func (h *Handlers) BearerGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("Authorization")
		tok := strings.TrimPrefix(raw, "Bearer ")
		if tok == raw || tok == "" {
			h.challenge(w)
			return
		}
		sub, err := h.store.Verify(tok)
		if err != nil {
			h.challenge(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithSub(r.Context(), sub)))
	})
}

func (h *Handlers) challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate",
		fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, h.publicURL))
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
}

// ---- helpers ----

func allowedRedirect(c *Client, redirectURI string) bool {
	if len(c.RedirectURIs) == 0 {
		return true // client registered without redirect URIs — be lenient
	}
	for _, u := range c.RedirectURIs {
		if u == redirectURI {
			return true
		}
	}
	return false
}

func issueAndWrite(ctx context.Context, w http.ResponseWriter, s *Store, clientID, sub, resource string, scopes []string) {
	toks, err := s.Issue(ctx, clientID, sub, resource, scopes)
	if err != nil {
		tokenErr(w, "server_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  toks.AccessToken,
		"token_type":    "Bearer",
		"expires_in":    toks.ExpiresIn,
		"refresh_token": toks.RefreshToken,
		"scope":         toks.Scope,
	})
}

func tokenErr(w http.ResponseWriter, code string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const loginHTML = `<!doctype html>
<html lang="pt-br"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Conectar WhatsApp</title>
<style>
 body{font-family:system-ui,sans-serif;background:#0b141a;color:#e9edef;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}
 .card{background:#111b21;padding:28px;border-radius:14px;max-width:360px;width:100%;text-align:center;box-shadow:0 8px 30px rgba(0,0,0,.4)}
 h1{font-size:18px;margin:0 0 6px}p{color:#8696a0;font-size:13px;margin:6px 0}
 img{width:256px;height:256px;background:#fff;border-radius:8px;margin:12px auto;display:block}
 .status{margin-top:12px;font-size:13px}.ok{color:#00a884}.err{color:#f15c6d}
 details{margin-top:14px;text-align:left}summary{cursor:pointer;color:#00a884;font-size:13px}
 input,button{font-size:14px;padding:8px;border-radius:6px;border:1px solid #2a3942;background:#202c33;color:#e9edef;margin-top:6px}
 button{background:#00a884;color:#111b21;border:0;cursor:pointer;width:100%}
 .code{font-size:22px;letter-spacing:3px;font-weight:700;color:#00a884;margin-top:8px}
</style></head><body>
<div class="card">
 <h1>Conectar WhatsApp</h1>
 <p>Abra o WhatsApp → <b>Aparelhos conectados</b> → <b>Conectar um aparelho</b> e escaneie o QR.</p>
 <img id="qr" alt="QR">
 <div class="status" id="status">Gerando QR…</div>
 <details><summary>Conectar com número de telefone</summary>
  <input id="num" placeholder="55DDDNÚMERO (só dígitos)" style="width:100%">
  <button onclick="reqCode()">Gerar código</button>
  <div class="code" id="paircode"></div>
 </details>
</div>
<script>
 const flow = {{.FlowID}};
 async function refreshQR(){
  try{const r=await fetch('/login/qr?authFlow='+encodeURIComponent(flow));const d=await r.json();
   if(d.base64){document.getElementById('qr').src=d.base64;}
   if(d.pairingCode){document.getElementById('paircode').textContent=d.pairingCode;}
  }catch(e){}
 }
 async function reqCode(){
  const num=document.getElementById('num').value;
  const r=await fetch('/login/pairing?authFlow='+encodeURIComponent(flow),{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({number:num})});
  const d=await r.json();
  document.getElementById('paircode').textContent=d.pairingCode||d.error||'';
 }
 async function poll(){
  try{const r=await fetch('/login/status?authFlow='+encodeURIComponent(flow));const d=await r.json();
   const s=document.getElementById('status');
   if(d.state==='open'&&d.redirect){s.className='status ok';s.textContent='Conectado! Redirecionando…';location.href=d.redirect;return;}
   if(d.state==='timeout'||d.state==='expired'||d.state==='error'){s.className='status err';s.textContent='Expirou/erro — recarregue para tentar de novo.';return;}
   s.textContent='Aguardando você escanear…';
  }catch(e){}
  setTimeout(poll,2500);
 }
 refreshQR();setInterval(refreshQR,2500);poll();
</script></body></html>`
