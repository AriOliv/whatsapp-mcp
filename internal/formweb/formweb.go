// Package formweb serves the pages of the web forms sent over WhatsApp
// (GET/POST /f/{token}). The link token is the only credential: it is single
// use, expires, and only its hash is stored. Pages are self-contained (no
// external assets), are not indexed and do not leak the URL as a referrer.
package formweb

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"html/template"
	"net/http"
	"time"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

//go:embed form.html
var files embed.FS

var page = template.Must(template.New("form.html").Funcs(template.FuncMap{
	"has": func(list []string, v string) bool {
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	},
}).ParseFS(files, "form.html"))

// Forms is the subset of wa.Manager the pages need.
type Forms interface {
	OpenForm(ctx context.Context, token string) (*appstore.Form, wa.FormSpec, error)
	SubmitForm(ctx context.Context, token string, raw map[string][]string) (map[string]string, error)
}

// Handler serves /f/{token}.
type Handler struct {
	forms  Forms
	secret []byte // keys the per-form CSRF token
}

// New returns the page handler. secret must be a server-side secret (the MCP
// JWT secret is reused).
func New(forms Forms, secret []byte) *Handler { return &Handler{forms: forms, secret: secret} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /f/{token}", h.get)
	mux.HandleFunc("POST /f/{token}", h.post)
}

type view struct {
	State  string // form | closed | expired | done | missing
	Spec   wa.FormSpec
	Values map[string][]string
	Errors map[string]string
	CSRF   string
}

func (h *Handler) csrf(token string) string {
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte("form-csrf:" + token))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Handler) render(w http.ResponseWriter, code int, v view) {
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Cache-Control", "no-store")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("X-Robots-Tag", "noindex, nofollow")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("X-Frame-Options", "DENY")
	hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(code)
	_ = page.Execute(w, v)
}

// state maps a stored form to the page shown for it, or "" if it can be filled.
func state(f *appstore.Form) string {
	switch {
	case f.Status == appstore.FormSubmitted:
		return "closed"
	case f.Expired(time.Now()):
		return "expired"
	}
	return ""
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	f, spec, err := h.forms.OpenForm(r.Context(), token)
	if err != nil {
		h.fail(w, err)
		return
	}
	if s := state(f); s != "" {
		h.render(w, http.StatusGone, view{State: s, Spec: spec})
		return
	}
	h.render(w, http.StatusOK, view{State: "form", Spec: spec, CSRF: h.csrf(token)})
}

func (h *Handler) post(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !hmac.Equal([]byte(r.PostForm.Get("_csrf")), []byte(h.csrf(token))) {
		http.Error(w, "invalid form", http.StatusForbidden)
		return
	}
	f, spec, err := h.forms.OpenForm(r.Context(), token)
	if err != nil {
		h.fail(w, err)
		return
	}
	if s := state(f); s != "" {
		h.render(w, http.StatusGone, view{State: s, Spec: spec})
		return
	}
	fieldErrs, err := h.forms.SubmitForm(r.Context(), token, r.PostForm)
	switch {
	case err == nil:
		h.render(w, http.StatusOK, view{State: "done", Spec: spec})
	case errors.Is(err, wa.ErrInvalidSpec):
		h.render(w, http.StatusUnprocessableEntity, view{State: "form", Spec: spec, Values: r.PostForm, Errors: fieldErrs, CSRF: h.csrf(token)})
	case errors.Is(err, wa.ErrFormClosed):
		h.render(w, http.StatusGone, view{State: "closed", Spec: spec})
	default:
		h.fail(w, err)
	}
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, wa.ErrFormNotFound) {
		h.render(w, http.StatusNotFound, view{State: "missing"})
		return
	}
	http.Error(w, "temporarily unavailable", http.StatusInternalServerError)
}
