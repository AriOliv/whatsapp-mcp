package wa

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

func dm(sender types.JID, text string) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: sender, Sender: sender},
			ID:            "MSG1", PushName: "Fulano", Timestamp: time.Unix(1700000000, 0),
		},
		Message: &waE2E.Message{Conversation: proto.String(text)},
	}
}

func TestInboundFilters(t *testing.T) {
	m := &Manager{webhook: &WebhookConfig{URL: "x", Secret: "s", Accounts: map[string]bool{"5521000000000": true}}}
	ctx := context.Background()
	user := types.NewJID("5511999999999", types.DefaultUserServer)
	msg := appstore.Message{Body: "oi"}

	ev := m.inboundFor(ctx, "5521000000000", dm(user, "oi"), msg)
	if ev == nil || ev.From != "5511999999999" || ev.Text != "oi" || ev.Type != "text" || ev.PushName != "Fulano" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if m.inboundFor(ctx, "5599999999999", dm(user, "oi"), msg) != nil {
		t.Fatal("account not in allowlist must not push")
	}
	mine := dm(user, "oi")
	mine.Info.IsFromMe = true
	if m.inboundFor(ctx, "5521000000000", mine, msg) != nil {
		t.Fatal("own messages must not push")
	}
	grp := dm(user, "oi")
	grp.Info.IsGroup = true
	grp.Info.Chat = types.NewJID("1203630", types.GroupServer)
	if m.inboundFor(ctx, "5521000000000", grp, msg) != nil {
		t.Fatal("group messages must not push")
	}
	if m.inboundFor(ctx, "5521000000000", dm(user, ""), appstore.Message{}) != nil {
		t.Fatal("empty (protocol/reaction) messages must not push")
	}
	// @lid sender with phone in SenderAlt
	lid := dm(types.NewJID("123456789", types.HiddenUserServer), "oi")
	lid.Info.SenderAlt = user
	if ev := m.inboundFor(ctx, "5521000000000", lid, msg); ev == nil || ev.From != "5511999999999" {
		t.Fatalf("lid sender must resolve via SenderAlt: %+v", ev)
	}
}

func TestOutboxDeliversSignedAndRetries(t *testing.T) {
	st := newTestStore(t)
	var calls atomic.Int32
	var mu sync.Mutex
	var got []InboundEvent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get("X-Timestamp"), 10, 64)
		if r.Header.Get("X-Signature") != Sign("segredo", ts, body) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable) // first attempt fails → retry
			return
		}
		var ev InboundEvent
		_ = json.Unmarshal(body, &ev)
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}))
	defer srv.Close()

	m := &Manager{store: st, log: &countingLog{}, outboxKick: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.SetWebhook(ctx, WebhookConfig{URL: srv.URL, Secret: "segredo", Accounts: map[string]bool{"1": true}}); err != nil {
		t.Fatal(err)
	}
	m.pushInbound(&InboundEvent{Account: "1", ID: "A", From: "5511", Text: "oi"})
	m.pushInbound(&InboundEvent{Account: "1", ID: "A", From: "5511", Text: "oi"}) // duplicate ignored
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].ID != "A" || got[0].Text != "oi" {
		t.Fatalf("expected one delivery after a retry, got %+v (calls=%d)", got, calls.Load())
	}
	items, _ := st.DueOutbox(ctx, 10)
	if len(items) != 0 {
		t.Fatalf("outbox not drained: %d", len(items))
	}
}

func TestParseAccounts(t *testing.T) {
	a := ParseAccounts(" +55 (21) 9999-0000, 5511888877777 ,")
	if !a["552199990000"] || !a["5511888877777"] || len(a) != 2 {
		t.Fatalf("%v", a)
	}
}
