package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

type fake struct {
	sent    []string
	buttons []wa.ButtonsSpec
	lists   []wa.ListSpec
	forms   []wa.FormSpec
	refuse  bool // simulate WhatsApp refusing the interactive send
}

func (f *fake) SendForm(_ context.Context, account, to string, spec wa.FormSpec) (wa.FormSent, error) {
	if spec.Title == "" {
		return wa.FormSent{}, fmt.Errorf("%w: title is required", wa.ErrInvalidSpec)
	}
	f.forms = append(f.forms, spec)
	return wa.FormSent{ID: "frm_1", Link: "https://x/f/tok", SendResult: wa.SendResult{ID: "FM1"}}, nil
}

func (f *fake) GetForm(_ context.Context, account, id string) (*wa.FormView, error) {
	if id != "frm_1" {
		return nil, wa.ErrFormNotFound
	}
	return &wa.FormView{ID: id, Status: "submitted", Answers: map[string]string{"nome": "Ari"}}, nil
}

func (f *fake) SendButtons(_ context.Context, account, to string, spec wa.ButtonsSpec, flavor string, fallback bool) (wa.SendResult, error) {
	if len(spec.Buttons) == 0 {
		return wa.SendResult{}, fmt.Errorf("%w: need buttons", wa.ErrInvalidSpec)
	}
	f.buttons = append(f.buttons, spec)
	if f.refuse {
		if !fallback {
			return wa.SendResult{}, errors.New("interactive send refused: 405")
		}
		return wa.SendResult{ID: "FB1", Fallback: true, Err: "405"}, nil
	}
	return wa.SendResult{ID: "BT1"}, nil
}

func (f *fake) SendList(_ context.Context, account, to string, spec wa.ListSpec, fallback bool) (wa.SendResult, error) {
	f.lists = append(f.lists, spec)
	return wa.SendResult{ID: "LS1"}, nil
}

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

func TestSendButtonsAndList(t *testing.T) {
	f := &fake{}
	mux := http.NewServeMux()
	New(f, tok, map[string]bool{"5521": true}).Register(mux)
	auth := "Bearer " + tok

	body := `{"account":"5521","to":"5511","text":"Escolha","title":"T","footer":"F",
		"buttons":[{"type":"quick_reply","text":"Sim","id":"sim"},{"type":"url","text":"Site","url":"https://avenia.io"}]}`
	w := do(t, mux, "POST", "/api/send/buttons", auth, body)
	if w.Code != 200 {
		t.Fatalf("buttons: %d %s", w.Code, w.Body)
	}
	var res sendResultResp
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.ID != "BT1" || res.Fallback {
		t.Fatalf("buttons result = %+v", res)
	}
	if got := f.buttons[0]; got.Title != "T" || got.Footer != "F" || len(got.Buttons) != 2 || got.Buttons[0].ID != "sim" || got.Buttons[1].URL != "https://avenia.io" {
		t.Fatalf("spec passed through = %+v", got)
	}

	list := `{"account":"5521","to":"5511","text":"Menu","buttonText":"Ver",
		"sections":[{"title":"A","rows":[{"id":"pix","title":"PIX"}]}]}`
	if w := do(t, mux, "POST", "/api/send/list", auth, list); w.Code != 200 || f.lists[0].Sections[0].Rows[0].ID != "pix" {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}

	// allowlist + auth still guard the new routes
	if w := do(t, mux, "POST", "/api/send/buttons", "", body); w.Code != 401 {
		t.Fatalf("no auth: %d", w.Code)
	}
	if w := do(t, mux, "POST", "/api/send/list", auth, `{"account":"9999"}`); w.Code != 403 {
		t.Fatalf("account outside allowlist: %d", w.Code)
	}
	// a bad spec is the caller's fault
	if w := do(t, mux, "POST", "/api/send/buttons", auth, `{"account":"5521","to":"5511","text":"x","buttons":[]}`); w.Code != 400 {
		t.Fatalf("invalid spec: %d", w.Code)
	}
	if w := do(t, mux, "POST", "/api/send/buttons", auth, `{not json`); w.Code != 400 {
		t.Fatalf("bad json: %d", w.Code)
	}

	// WhatsApp refusing: 502 without fallback, 200 + fallback flag with it
	f.refuse = true
	if w := do(t, mux, "POST", "/api/send/buttons", auth, body); w.Code != 502 {
		t.Fatalf("refused, no fallback: %d", w.Code)
	}
	withFB := strings.Replace(body, `"text":"Escolha"`, `"text":"Escolha","fallbackText":true`, 1)
	w = do(t, mux, "POST", "/api/send/buttons", auth, withFB)
	res = sendResultResp{}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if w.Code != 200 || !res.Fallback || res.ID != "FB1" || res.FallbackReason != "405" {
		t.Fatalf("refused with fallback: %d %+v", w.Code, res)
	}
}

func TestSendAndGetForm(t *testing.T) {
	f := &fake{}
	mux := http.NewServeMux()
	New(f, tok, map[string]bool{"5521": true}).Register(mux)
	auth := "Bearer " + tok

	body := `{"account":"5521","to":"5511","title":"Cadastro","fields":[{"id":"nome","label":"Nome","required":true}]}`
	w := do(t, mux, "POST", "/api/send/form", auth, body)
	if w.Code != 200 {
		t.Fatalf("send form: %d %s", w.Code, w.Body)
	}
	var sent map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &sent)
	if sent["form_id"] != "frm_1" || sent["id"] != "FM1" || sent["link"] == nil {
		t.Fatalf("send form response = %v", sent)
	}
	if len(f.forms) != 1 || f.forms[0].Fields[0].ID != "nome" {
		t.Fatalf("spec not passed through: %+v", f.forms)
	}
	if w := do(t, mux, "POST", "/api/send/form", auth, `{"account":"5521","to":"5511","fields":[]}`); w.Code != 400 {
		t.Fatalf("invalid spec = %d", w.Code)
	}
	if w := do(t, mux, "POST", "/api/send/form", auth, `{"account":"9999","to":"5511","title":"x"}`); w.Code != 403 {
		t.Fatalf("other account = %d", w.Code)
	}

	w = do(t, mux, "GET", "/api/forms/5521/frm_1", auth, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"nome":"Ari"`) {
		t.Fatalf("get form: %d %s", w.Code, w.Body)
	}
	if w := do(t, mux, "GET", "/api/forms/5521/frm_x", auth, ""); w.Code != 404 {
		t.Fatalf("unknown form = %d", w.Code)
	}
	if w := do(t, mux, "GET", "/api/forms/9999/frm_1", auth, ""); w.Code != 403 {
		t.Fatalf("foreign account = %d", w.Code)
	}
}
