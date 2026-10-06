package formweb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

type fakeForms struct {
	form      appstore.Form
	submitted map[string][]string
}

var spec = wa.FormSpec{Title: "Cadastro <b>", Fields: []wa.FormField{
	{ID: "nome", Label: "Nome", Type: "text", Required: true},
	{ID: "plano", Label: "Plano", Type: "select", Options: []string{"basico", "pro"}},
}}

func (f *fakeForms) OpenForm(_ context.Context, token string) (*appstore.Form, wa.FormSpec, error) {
	if token != "good" {
		return nil, wa.FormSpec{}, wa.ErrFormNotFound
	}
	c := f.form
	return &c, spec, nil
}

func (f *fakeForms) SubmitForm(_ context.Context, _ string, raw map[string][]string) (map[string]string, error) {
	if f.form.Status == appstore.FormSubmitted {
		return nil, wa.ErrFormClosed
	}
	if strings.TrimSpace(strings.Join(raw["nome"], "")) == "" {
		return map[string]string{"nome": "Campo obrigatório."}, fmt.Errorf("%w: bad", wa.ErrInvalidSpec)
	}
	f.submitted = raw
	f.form.Status = appstore.FormSubmitted
	return nil, nil
}

func setup() (*fakeForms, *http.ServeMux, *Handler) {
	ff := &fakeForms{form: appstore.Form{ID: "frm_1", Status: appstore.FormOpen, ExpiresAt: time.Now().Add(time.Hour)}}
	h := New(ff, []byte("0123456789abcdef0123456789abcdef"))
	mux := http.NewServeMux()
	h.Register(mux)
	return ff, mux, h
}

func post(mux *http.ServeMux, token string, v url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/f/"+token, strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestFormPage(t *testing.T) {
	ff, mux, h := setup()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/f/good", nil))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `name="nome"`) || !strings.Contains(body, h.csrf("good")) {
		t.Fatalf("GET = %d\n%s", w.Code, body)
	}
	if strings.Contains(body, "<b>") || !strings.Contains(body, "Cadastro &lt;b&gt;") {
		t.Fatal("title must be escaped")
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("headers = %v", w.Header())
	}

	if w := post(mux, "good", url.Values{"nome": {"Ari"}}); w.Code != 403 {
		t.Fatalf("no csrf = %d", w.Code)
	}
	if w := post(mux, "good", url.Values{"_csrf": {h.csrf("good")}, "plano": {"pro"}}); w.Code != 422 ||
		!strings.Contains(w.Body.String(), "Campo obrigatório.") || !strings.Contains(w.Body.String(), "<option selected>pro</option>") {
		t.Fatalf("invalid = %d\n%s", w.Code, w.Body.String())
	}
	if w := post(mux, "good", url.Values{"_csrf": {h.csrf("good")}, "nome": {"Ari"}}); w.Code != 200 ||
		!strings.Contains(w.Body.String(), "Respostas enviadas") || ff.submitted["nome"][0] != "Ari" {
		t.Fatalf("valid = %d", w.Code)
	}
	if w := post(mux, "good", url.Values{"_csrf": {h.csrf("good")}, "nome": {"Ari"}}); w.Code != 410 ||
		!strings.Contains(w.Body.String(), "já enviado") {
		t.Fatalf("resubmit = %d", w.Code)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/f/good", nil))
	if w.Code != 410 {
		t.Fatalf("GET after submit = %d", w.Code)
	}
}

func TestFormPageMissingAndExpired(t *testing.T) {
	ff, mux, _ := setup()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/f/bad", nil))
	if w.Code != 404 || !strings.Contains(w.Body.String(), "Link inválido") {
		t.Fatalf("missing = %d", w.Code)
	}
	ff.form.ExpiresAt = time.Now().Add(-time.Minute)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/f/good", nil))
	if w.Code != 410 || !strings.Contains(w.Body.String(), "Link expirado") {
		t.Fatalf("expired = %d", w.Code)
	}
}
