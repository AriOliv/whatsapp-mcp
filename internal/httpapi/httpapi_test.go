package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fake struct{ sent []string }

func (f *fake) SendText(_ context.Context, account, to, text string) (string, error) {
	f.sent = append(f.sent, account+">"+to+":"+text)
	return "ID1", nil
}
func (f *fake) SendMedia(context.Context, string, string, string, string, string, string, string) (string, error) {
	return "ID2", nil
}
func (f *fake) ChatPresence(context.Context, string, string, string) error       { return nil }
func (f *fake) MarkRead(context.Context, string, string, string, []string) error { return nil }
func (f *fake) HasDevice(string) bool                                            { return true }
func (f *fake) DownloadMedia(context.Context, string, string) ([]byte, string, string, error) {
	return []byte("OGG"), "audio/ogg", "a.ogg", nil
}

const tok = "0123456789abcdef0123456789"

func do(t *testing.T, mux *http.ServeMux, method, path, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestGuardAndAllowlist(t *testing.T) {
	if New(&fake{}, "short", map[string]bool{"1": true}) != nil {
		t.Fatal("short token must disable the API")
	}
	f := &fake{}
	mux := http.NewServeMux()
	New(f, tok, map[string]bool{"5521": true}).Register(mux)
	if w := do(t, mux, "POST", "/api/send", "", `{}`); w.Code != 401 {
		t.Fatalf("no auth: %d", w.Code)
	}
	if w := do(t, mux, "POST", "/api/send", "Bearer x", `{}`); w.Code != 401 {
		t.Fatalf("bad auth: %d", w.Code)
	}
	if w := do(t, mux, "POST", "/api/send", "Bearer "+tok, `{"account":"9999","to":"55","text":"oi"}`); w.Code != 403 {
		t.Fatalf("account outside allowlist: %d", w.Code)
	}
	w := do(t, mux, "POST", "/api/send", "Bearer "+tok, `{"account":"5521","to":"5511","text":"oi"}`)
	if w.Code != 200 || len(f.sent) != 1 || f.sent[0] != "5521>5511:oi" {
		t.Fatalf("send: %d %v", w.Code, f.sent)
	}
	w = do(t, mux, "GET", "/api/media/5521/ABC", "Bearer "+tok, "")
	if w.Code != 200 || w.Body.String() != "OGG" || w.Header().Get("Content-Type") != "audio/ogg" {
		t.Fatalf("media: %d %q", w.Code, w.Body.String())
	}
	if w := do(t, mux, "GET", "/api/media/9999/ABC", "Bearer "+tok, ""); w.Code != 403 {
		t.Fatalf("media other account: %d", w.Code)
	}
}
