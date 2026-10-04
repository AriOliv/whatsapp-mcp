// Package httpapi is a small REST surface for a trusted backend (e.g. a service
// that uses one paired number as its WhatsApp front door). It is separate from the
// per-user OAuth /mcp endpoint: a single static bearer token, and only the
// accounts listed in GATEWAY_ACCOUNTS can be driven through it.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Sender is the subset of wa.Manager the API needs (an interface keeps it testable).
type Sender interface {
	SendText(ctx context.Context, account, to, text string) (string, error)
	SendMedia(ctx context.Context, account, to, kind, data, caption, mimeType, fileName string) (string, error)
	ChatPresence(ctx context.Context, account, to, state string) error
	MarkRead(ctx context.Context, account, chat, sender string, ids []string) error
	DownloadMedia(ctx context.Context, account, msgID string) ([]byte, string, string, error)
	HasDevice(sub string) bool
}

// API serves /api/*.
type API struct {
	s        Sender
	token    []byte
	accounts map[string]bool
}

// New returns nil when the token is unset (API disabled).
func New(s Sender, token string, accounts map[string]bool) *API {
	if len(token) < 24 || len(accounts) == 0 {
		return nil
	}
	return &API{s: s, token: []byte("Bearer " + token), accounts: accounts}
}

// Register mounts the routes on mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/send", a.guard(a.send))
	mux.HandleFunc("POST /api/presence", a.guard(a.presence))
	mux.HandleFunc("POST /api/read", a.guard(a.read))
	mux.HandleFunc("GET /api/media/{account}/{id}", a.guard(a.media))
	mux.HandleFunc("GET /api/status/{account}", a.guard(a.status))
}

func (a *API) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), a.token) != 1 {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r)
	}
}

func (a *API) allowed(w http.ResponseWriter, account string) bool {
	if !a.accounts[account] {
		writeErr(w, http.StatusForbidden, "account not enabled for the gateway API")
		return false
	}
	return true
}

type sendReq struct {
	Account  string `json:"account"`
	To       string `json:"to"`
	Text     string `json:"text"`
	Kind     string `json:"kind,omitempty"` // image|video|document|audio — with Data
	Data     string `json:"data,omitempty"` // base64 or http(s) URL
	MimeType string `json:"mimetype,omitempty"`
	FileName string `json:"filename,omitempty"`
}

func (a *API) send(w http.ResponseWriter, r *http.Request) {
	var req sendReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !a.allowed(w, req.Account) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var id string
	var err error
	switch {
	case req.Data != "":
		id, err = a.s.SendMedia(ctx, req.Account, req.To, req.Kind, req.Data, req.Text, req.MimeType, req.FileName)
	case strings.TrimSpace(req.Text) != "":
		id, err = a.s.SendText(ctx, req.Account, req.To, req.Text)
	default:
		writeErr(w, http.StatusBadRequest, "text or data required")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]string{"id": id})
}

func (a *API) presence(w http.ResponseWriter, r *http.Request) {
	var req struct{ Account, To, State string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !a.allowed(w, req.Account) {
		return
	}
	if err := a.s.ChatPresence(r.Context(), req.Account, req.To, req.State); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) read(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Account, Chat, Sender string
		IDs                   []string `json:"ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !a.allowed(w, req.Account) {
		return
	}
	if err := a.s.MarkRead(r.Context(), req.Account, req.Chat, req.Sender, req.IDs); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) media(w http.ResponseWriter, r *http.Request) {
	account := r.PathValue("account")
	if !a.allowed(w, account) {
		return
	}
	data, mime, name, err := a.s.DownloadMedia(r.Context(), account, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	if name != "" {
		w.Header().Set("X-File-Name", name)
	}
	_, _ = w.Write(data)
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	account := r.PathValue("account")
	if !a.allowed(w, account) {
		return
	}
	writeJSON(w, map[string]bool{"connected": a.s.HasDevice(account)})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
