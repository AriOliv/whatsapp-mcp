// Package httpapi is a small REST surface for a trusted backend (e.g. a service
// that uses one paired number as its WhatsApp front door). It is separate from the
// per-user OAuth /mcp endpoint: a single static bearer token, and only the
// accounts listed in GATEWAY_ACCOUNTS can be driven through it.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

// Sender is the subset of wa.Manager the API needs (an interface keeps it testable).
type Sender interface {
	SendText(ctx context.Context, account, to, text string) (string, error)
	SendMedia(ctx context.Context, account, to, kind, data, caption, mimeType, fileName string) (string, error)
	SendButtons(ctx context.Context, account, to string, spec wa.ButtonsSpec, flavor string, fallback bool) (wa.SendResult, error)
	SendList(ctx context.Context, account, to string, spec wa.ListSpec, fallback bool) (wa.SendResult, error)
	SendForm(ctx context.Context, account, to string, spec wa.FormSpec) (wa.FormSent, error)
	GetForm(ctx context.Context, account, id string) (*wa.FormView, error)
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
	mux.HandleFunc("POST /api/send/buttons", a.guard(a.sendButtons))
	mux.HandleFunc("POST /api/send/list", a.guard(a.sendList))
	mux.HandleFunc("POST /api/send/form", a.guard(a.sendForm))
	mux.HandleFunc("GET /api/forms/{account}/{id}", a.guard(a.getForm))
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

type sendButtonsReq struct {
	Account      string      `json:"account"`
	To           string      `json:"to"`
	Text         string      `json:"text"`
	Title        string      `json:"title,omitempty"`
	Footer       string      `json:"footer,omitempty"`
	Buttons      []wa.Button `json:"buttons"`
	Flavor       string      `json:"flavor,omitempty"`       // mixed (default) | full
	FallbackText bool        `json:"fallbackText,omitempty"` // send as plain text if WhatsApp refuses
}

type sendListReq struct {
	Account      string           `json:"account"`
	To           string           `json:"to"`
	Text         string           `json:"text"`
	ButtonText   string           `json:"buttonText"`
	Title        string           `json:"title,omitempty"`
	Footer       string           `json:"footer,omitempty"`
	Sections     []wa.ListSection `json:"sections"`
	FallbackText bool             `json:"fallbackText,omitempty"`
}

type sendResultResp struct {
	ID             string `json:"id"`
	Fallback       bool   `json:"fallback,omitempty"`
	FallbackReason string `json:"fallback_reason,omitempty"`
}

func (a *API) sendButtons(w http.ResponseWriter, r *http.Request) {
	var req sendButtonsReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !a.allowed(w, req.Account) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	spec := wa.ButtonsSpec{Text: req.Text, Title: req.Title, Footer: req.Footer, Buttons: req.Buttons}
	res, err := a.s.SendButtons(ctx, req.Account, req.To, spec, req.Flavor, req.FallbackText)
	a.writeSendResult(w, res, err)
}

func (a *API) sendList(w http.ResponseWriter, r *http.Request) {
	var req sendListReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !a.allowed(w, req.Account) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	spec := wa.ListSpec{Text: req.Text, Title: req.Title, Footer: req.Footer, ButtonText: req.ButtonText, Sections: req.Sections}
	res, err := a.s.SendList(ctx, req.Account, req.To, spec, req.FallbackText)
	a.writeSendResult(w, res, err)
}

type sendFormReq struct {
	Account string `json:"account"`
	To      string `json:"to"`
	wa.FormSpec
}

func (a *API) sendForm(w http.ResponseWriter, r *http.Request) {
	var req sendFormReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !a.allowed(w, req.Account) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	res, err := a.s.SendForm(ctx, req.Account, req.To, req.FormSpec)
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, wa.ErrInvalidSpec) {
			code = http.StatusBadRequest
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, res)
}

func (a *API) getForm(w http.ResponseWriter, r *http.Request) {
	account := r.PathValue("account")
	if !a.allowed(w, account) {
		return
	}
	v, err := a.s.GetForm(r.Context(), account, r.PathValue("id"))
	switch {
	case errors.Is(err, wa.ErrFormNotFound):
		writeErr(w, http.StatusNotFound, "form not found")
	case err != nil:
		writeErr(w, http.StatusBadGateway, err.Error())
	default:
		writeJSON(w, v)
	}
}

// writeSendResult maps an interactive send to HTTP: spec validation errors are
// the caller's fault (400); a refusal by WhatsApp or a transport error is 502.
func (a *API) writeSendResult(w http.ResponseWriter, res wa.SendResult, err error) {
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, wa.ErrInvalidSpec) {
			code = http.StatusBadRequest
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, sendResultResp{ID: res.ID, Fallback: res.Fallback, FallbackReason: res.Err})
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
