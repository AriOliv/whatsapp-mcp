package wa

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

func TestHistorySyncWorkerIngestsAndIsolatesAccounts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dsn := filepath.Join(t.TempDir(), "test.db") + "?_pragma=foreign_keys(1)"
	st, _, err := appstore.Open(dsn, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}

	acctAJID := types.NewJID("5511111111111", types.DefaultUserServer)
	acctBJID := types.NewJID("5522222222222", types.DefaultUserServer)
	cliA := &whatsmeow.Client{Store: &store.Device{ID: &acctAJID}}
	cliB := &whatsmeow.Client{Store: &store.Device{ID: &acctBJID}}

	m := &Manager{
		store:     st,
		clients:   map[string]*whatsmeow.Client{"5511111111111": cliA, "5522222222222": cliB},
		loggedOut: map[string]time.Time{},
		log:       waLog.Noop,
	}
	m.startHistorySync(ctx)

	chatJID := "5511988887777@s.whatsapp.net"

	// Pre-populate an undecryptable placeholder that HistorySync should upgrade.
	if err := st.SavePlaceholder(ctx, "5511111111111", appstore.Message{
		ID:      "hist_1",
		ChatJID: chatJID,
		TS:      1700001000000,
		Body:    undecryptableBody,
	}); err != nil {
		t.Fatalf("SavePlaceholder: %v", err)
	}

	blob := &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_RECENT.Enum(),
		Pushnames: []*waHistorySync.Pushname{
			{ID: proto.String(chatJID), Pushname: proto.String("Mariana Push")},
		},
		Conversations: []*waHistorySync.Conversation{
			{
				ID: proto.String(chatJID),
				Messages: []*waHistorySync.HistorySyncMsg{
					{
						Message: &waWeb.WebMessageInfo{
							Key: &waCommon.MessageKey{
								RemoteJID: proto.String(chatJID),
								FromMe:    proto.Bool(false),
								ID:        proto.String("hist_1"),
							},
							MessageTimestamp: proto.Uint64(1700001000),
							Message: &waE2E.Message{
								Conversation: proto.String("texto recuperado pelo HistorySync"),
							},
						},
					},
					{
						Message: &waWeb.WebMessageInfo{
							Key: &waCommon.MessageKey{
								RemoteJID: proto.String(chatJID),
								FromMe:    proto.Bool(false),
								ID:        proto.String("hist_media_1"),
							},
							MessageTimestamp: proto.Uint64(1700002000),
							Message: &waE2E.Message{
								EphemeralMessage: &waE2E.FutureProofMessage{
									Message: &waE2E.Message{
										DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
											Message: &waE2E.Message{
												DocumentMessage: &waE2E.DocumentMessage{
													URL:           proto.String("https://mmg.whatsapp.net/d/f/hist.enc"),
													DirectPath:    proto.String("/v/t62/hist.enc"),
													MediaKey:      []byte("0123456789abcdef0123456789abcdef"),
													FileSHA256:    []byte("sha"),
													FileEncSHA256: []byte("encsha"),
													FileLength:    proto.Uint64(2048),
													Mimetype:      proto.String("application/pdf"),
													FileName:      proto.String("relatorio.pdf"),
													Caption:       proto.String("Relatório histórico"),
													JPEGThumbnail: bytes.Repeat([]byte{0xaa}, 4096),
												},
											},
										},
									},
								},
							},
						},
					},
					{
						Message: &waWeb.WebMessageInfo{
							Key: &waCommon.MessageKey{
								RemoteJID: proto.String(chatJID),
								FromMe:    proto.Bool(false),
								ID:        proto.String("hist_vo_1"),
							},
							MessageTimestamp: proto.Uint64(1700003000),
							Message: &waE2E.Message{
								ViewOnceMessageV2: &waE2E.FutureProofMessage{
									Message: &waE2E.Message{
										ImageMessage: &waE2E.ImageMessage{
											Caption:    proto.String("segredo view once"),
											DirectPath: proto.String("/v/t62/vo.enc"),
											MediaKey:   []byte("0123456789abcdef0123456789abcdef"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Dispatch twice to verify idempotency.
	m.handler(cliA)(&events.HistorySync{Data: blob})
	m.handler(cliA)(&events.HistorySync{Data: blob})
	cancel()
	time.Sleep(50 * time.Millisecond)

	msgsA, err := m.ListMessages(context.Background(), "5511111111111", chatJID, 10)
	if err != nil {
		t.Fatalf("ListMessages(acctA): %v", err)
	}
	if len(msgsA) != 3 {
		t.Fatalf("expected 3 messages in Account A, got %d: %+v", len(msgsA), msgsA)
	}
	// Ordered newest first: hist_vo_1 (3000s), hist_media_1 (2000s), hist_1 (1000s).
	if msgsA[0].ID != "hist_vo_1" || msgsA[0].Body != viewOnceBody || msgsA[0].MediaType != "view_once" {
		t.Fatalf("unexpected ViewOnce row: %+v", msgsA[0])
	}
	if voProto, _, _ := st.GetMedia(context.Background(), "5511111111111", "hist_vo_1"); len(voProto) != 0 {
		t.Fatalf("ViewOnce in HistorySync must not persist media keys, got %d bytes", len(voProto))
	}
	if msgsA[1].ID != "hist_media_1" || msgsA[1].Body != "Relatório histórico" || msgsA[1].MediaType != "document" {
		t.Fatalf("unexpected media row: %+v", msgsA[1])
	}
	docProto, mtype, err := st.GetMedia(context.Background(), "5511111111111", "hist_media_1")
	if err != nil || len(docProto) == 0 || len(docProto) > 512 || mtype != "document" {
		t.Fatalf("historical media proto = (%d bytes, %q, err=%v), want slim document proto", len(docProto), mtype, err)
	}
	if msgsA[2].ID != "hist_1" || msgsA[2].Body != "texto recuperado pelo HistorySync" {
		t.Fatalf("placeholder was not upgraded by HistorySync: %+v", msgsA[2])
	}
	if gotName := m.ChatName(context.Background(), "5511111111111", chatJID); gotName != "Mariana Push" {
		t.Fatalf("ChatName(acctA) = %q, want Mariana Push", gotName)
	}

	// OldestMessage returns hist_1 for Account A.
	oldest, err := st.OldestMessage(context.Background(), "5511111111111", []string{chatJID})
	if err != nil || oldest == nil || oldest.ID != "hist_1" {
		t.Fatalf("OldestMessage(acctA) = %+v, err=%v, want hist_1", oldest, err)
	}

	// NEGATIVE ISOLATION TEST: Account B cannot see any of Account A's HistorySync data.
	msgsB, err := m.ListMessages(context.Background(), "5522222222222", chatJID, 10)
	if err != nil || len(msgsB) != 0 {
		t.Fatalf("cross-account HistorySync leak: Account B got messages %+v (err=%v)", msgsB, err)
	}
	if rawB, _, _ := st.GetMedia(context.Background(), "5522222222222", "hist_media_1"); len(rawB) != 0 {
		t.Fatalf("cross-account HistorySync media leak: Account B got %d bytes", len(rawB))
	}
	if oldestB, _ := st.OldestMessage(context.Background(), "5522222222222", []string{chatJID}); oldestB != nil {
		t.Fatalf("cross-account OldestMessage leak: Account B got %+v", oldestB)
	}
}

func TestHistorySyncQueueFullDropsWithoutBlocking(t *testing.T) {
	m := &Manager{
		history: make(chan historyJob, historyQueueSize),
		log:     waLog.Noop,
	}
	blob := &waHistorySync.HistorySync{}
	for i := 0; i < historyQueueSize; i++ {
		m.enqueueHistorySync("5511111111111", nil, blob)
	}
	if got := m.historyDropped.Load(); got != 0 {
		t.Fatalf("dropped = %d before queue full, want 0", got)
	}

	start := time.Now()
	for i := 0; i < 10; i++ {
		m.enqueueHistorySync("5511111111111", nil, blob)
	}
	if took := time.Since(start); took > 50*time.Millisecond {
		t.Fatalf("enqueueHistorySync blocked when full: took %s", took)
	}
	if got := m.historyDropped.Load(); got != 10 {
		t.Fatalf("historyDropped = %d, want 10", got)
	}
}

func TestHistorySyncKillSwitchAndPerChatCap(t *testing.T) {
	for _, v := range []string{"off", "OFF", "false", "0", "no"} {
		if !isHistorySyncDisabled(v) {
			t.Fatalf("isHistorySyncDisabled(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "on", "true", "1"} {
		if isHistorySyncDisabled(v) {
			t.Fatalf("isHistorySyncDisabled(%q) = true, want false", v)
		}
	}

	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "test.db") + "?_pragma=foreign_keys(1)"
	st, _, err := appstore.Open(dsn, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}

	// Kill switch active: enqueue and RequestHistorySync are rejected/no-op.
	mOff := &Manager{
		store:          st,
		historySyncOff: true,
		history:        make(chan historyJob, historyQueueSize),
		log:            waLog.Noop,
	}
	mOff.enqueueHistorySync("5511111111111", nil, &waHistorySync.HistorySync{})
	if len(mOff.history) != 0 {
		t.Fatal("expected enqueueHistorySync to be a no-op when historySyncOff=true")
	}
	if _, err := mOff.RequestHistorySync(ctx, "5511111111111", "5511988887777@s.whatsapp.net", 50); err == nil || !strings.Contains(err.Error(), "WA_HISTORY_SYNC=off") {
		t.Fatalf("expected WA_HISTORY_SYNC=off error, got %v", err)
	}

	// Per-chat cap: a conversation with historyMaxPerChat + 20 messages only
	// stores the newest historyMaxPerChat messages.
	chatJID := "5511988887777@s.whatsapp.net"
	total := historyMaxPerChat + 20
	hmList := make([]*waHistorySync.HistorySyncMsg, 0, total)
	for i := 1; i <= total; i++ {
		hmList = append(hmList, &waHistorySync.HistorySyncMsg{
			Message: &waWeb.WebMessageInfo{
				Key: &waCommon.MessageKey{
					RemoteJID: proto.String(chatJID),
					FromMe:    proto.Bool(false),
					ID:        proto.String(fmt.Sprintf("m_%04d", i)),
				},
				MessageTimestamp: proto.Uint64(uint64(1700000000 + i)),
				Message:          &waE2E.Message{Conversation: proto.String(fmt.Sprintf("msg %d", i))},
			},
		})
	}
	mCap := &Manager{store: st, log: waLog.Noop}
	mCap.processHistorySync(historyJob{
		account: "5511111111111",
		data: &waHistorySync.HistorySync{
			Conversations: []*waHistorySync.Conversation{
				{ID: proto.String(chatJID), Messages: hmList},
			},
		},
	})
	stored, err := st.ListMessages(ctx, "5511111111111", chatJID, 500)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(stored) != historyMaxPerChat {
		t.Fatalf("stored = %d, want capped %d", len(stored), historyMaxPerChat)
	}
	// Oldest stored should be m_0021 (since 1..20 were dropped by the newest-500 cap).
	oldest, err := st.OldestMessage(ctx, "5511111111111", []string{chatJID})
	if err != nil || oldest == nil || oldest.ID != "m_0021" {
		t.Fatalf("oldest after cap = %+v (err=%v), want m_0021", oldest, err)
	}
}
