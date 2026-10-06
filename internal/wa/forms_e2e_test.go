package wa

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

// A form goes from link to answers through the real store: open, invalid
// submit, valid submit, then closed for good.
func TestFormSubmitThroughStore(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "t.db") + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	st, _, err := appstore.Open(dsn, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InitForms(ctx); err != nil {
		t.Fatal(err)
	}
	m := &Manager{store: st}
	spec := testSpec()
	f := false
	spec.Receipt = &f // no client in this test
	_ = validateFormSpec(&spec)
	raw, _ := json.Marshal(spec)
	now := time.Now()
	if err := st.CreateForm(ctx, appstore.Form{ID: "frm_x", TokenHash: FormTokenHash("tok"), Account: "acct",
		To: "5521@s.whatsapp.net", Spec: raw, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := m.OpenForm(ctx, "wrong"); !errors.Is(err, ErrFormNotFound) {
		t.Fatalf("wrong token: %v", err)
	}
	if errs, err := m.SubmitForm(ctx, "tok", map[string][]string{"nome": {"Ari"}}); !errors.Is(err, ErrInvalidSpec) || errs["cpf"] == "" {
		t.Fatalf("invalid submit: %v %v", errs, err)
	}
	good := map[string][]string{"nome": {"Ari"}, "cpf": {"52998224725"}, "plano": {"basico"}, "aceite": {"on"}}
	if _, err := m.SubmitForm(ctx, "tok", good); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SubmitForm(ctx, "tok", good); !errors.Is(err, ErrFormClosed) {
		t.Fatalf("second submit: %v", err)
	}
	v, err := m.GetForm(ctx, "acct", "frm_x")
	if err != nil || v.Status != "submitted" || v.Answers["cpf"] != "52998224725" || v.To != "5521" {
		t.Fatalf("get = %+v %v", v, err)
	}
	if _, err := m.GetForm(ctx, "other", "frm_x"); !errors.Is(err, ErrFormNotFound) {
		t.Fatalf("other account: %v", err)
	}
}
