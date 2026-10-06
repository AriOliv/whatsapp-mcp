package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFormLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.InitForms(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := st.CreateForm(ctx, Form{ID: "f1", TokenHash: "h1", Account: "acct", To: "5511", Spec: []byte(`{}`),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f, err := st.FormByToken(ctx, "h1")
	if err != nil || f == nil || f.ID != "f1" || f.Status != FormOpen || f.Expired(now) {
		t.Fatalf("by token = %+v, %v", f, err)
	}
	if f, _ := st.FormByToken(ctx, "nope"); f != nil {
		t.Fatal("unknown token must return nil")
	}
	if f, _ := st.GetForm(ctx, "other", "f1"); f != nil {
		t.Fatal("another account must not see the form")
	}

	if err := st.SubmitForm(ctx, "f1", []byte(`{"a":"1"}`), now); err != nil {
		t.Fatal(err)
	}
	if err := st.SubmitForm(ctx, "f1", []byte(`{"a":"2"}`), now); !errors.Is(err, ErrFormClosed) {
		t.Fatalf("second submit: want ErrFormClosed, got %v", err)
	}
	f, _ = st.GetForm(ctx, "acct", "f1")
	if f.Status != FormSubmitted || string(f.Answers) != `{"a":"1"}` || f.SubmittedAt.IsZero() {
		t.Fatalf("after submit = %+v", f)
	}
}

func TestFormExpiryAndPurge(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	_ = st.InitForms(ctx)
	now := time.Now()
	_ = st.CreateForm(ctx, Form{ID: "old", TokenHash: "h", Account: "a", To: "t", Spec: []byte(`{}`),
		CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)})
	if err := st.SubmitForm(ctx, "old", []byte(`{}`), now); !errors.Is(err, ErrFormClosed) {
		t.Fatalf("expired submit: %v", err)
	}
	if n, _ := st.PurgeForms(ctx, 2*time.Hour, now); n != 0 {
		t.Fatalf("purged %d, nothing is that old yet", n)
	}
	if n, _ := st.PurgeForms(ctx, 30*time.Minute, now); n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}
}
