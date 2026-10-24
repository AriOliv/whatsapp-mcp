package wa

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

func TestUnwrapAndSlimMediaProtoStripsThumbnails(t *testing.T) {
	innerDoc := &waE2E.DocumentMessage{
		URL:           proto.String("https://mmg.whatsapp.net/d/f/doc.enc"),
		DirectPath:    proto.String("/v/t62.7119-24/doc.enc"),
		MediaKey:      []byte("0123456789abcdef0123456789abcdef"),
		FileSHA256:    []byte("sha256-plain"),
		FileEncSHA256: []byte("sha256-enc"),
		FileLength:    proto.Uint64(4096),
		Mimetype:      proto.String("application/pdf"),
		FileName:      proto.String("contrato.pdf"),
		Caption:       proto.String("Segue o contrato assinado"),
		JPEGThumbnail: bytes.Repeat([]byte{0xff}, 8192), // must NOT be persisted in Postgres
	}
	wrapped := &waE2E.Message{
		DeviceSentMessage: &waE2E.DeviceSentMessage{
			Message: &waE2E.Message{
				EphemeralMessage: &waE2E.FutureProofMessage{
					Message: &waE2E.Message{
						DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
							Message: &waE2E.Message{DocumentMessage: innerDoc},
						},
					},
				},
			},
		},
	}

	if got := mediaType(wrapped); got != "document" {
		t.Fatalf("mediaType(wrapped) = %q, want document", got)
	}
	if got := messageText(wrapped); got != "Segue o contrato assinado" {
		t.Fatalf("messageText(wrapped) = %q, want caption", got)
	}

	raw, err := slimMediaProto(wrapped)
	if err != nil {
		t.Fatalf("slimMediaProto: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("expected non-empty slimMediaProto")
	}
	if len(raw) > 512 {
		t.Fatalf("slimMediaProto should strip thumbnail bytes, got %d bytes", len(raw))
	}

	var decoded waE2E.Message
	if err := proto.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal slim proto: %v", err)
	}
	if len(decoded.GetDocumentMessage().GetJPEGThumbnail()) != 0 {
		t.Fatal("JPEGThumbnail must be stripped from stored media proto")
	}
	dl, mime, name := mediaPart(&decoded, "MSG123")
	if dl == nil || mime != "application/pdf" || name != "contrato.pdf" {
		t.Fatalf("mediaPart = (%v, %q, %q), want (non-nil, application/pdf, contrato.pdf)", dl, mime, name)
	}
	if dl.GetDirectPath() != "/v/t62.7119-24/doc.enc" || string(dl.GetMediaKey()) != string(innerDoc.MediaKey) {
		t.Fatalf("media keys mismatch after round-trip: directPath=%q", dl.GetDirectPath())
	}
}

func TestViewOnceDoesNotPersistContentOrMediaKeys(t *testing.T) {
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

	secretImg := &waE2E.ImageMessage{
		Caption:       proto.String("segredo confidencial"),
		DirectPath:    proto.String("/v/t62/secret.enc"),
		MediaKey:      []byte("secret-media-key-32-bytes-long!!"),
		FileSHA256:    []byte("sha"),
		FileEncSHA256: []byte("encsha"),
		Mimetype:      proto.String("image/jpeg"),
	}
	rawWire := &waE2E.Message{
		ViewOnceMessageV2: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{ImageMessage: secretImg},
		},
	}

	if !isViewOnceMessage(rawWire) {
		t.Fatal("expected isViewOnceMessage=true for ViewOnceMessageV2")
	}
	if got := messageText(rawWire); got != viewOnceBody {
		t.Fatalf("messageText(viewOnce) = %q, want %q", got, viewOnceBody)
	}
	if raw, _ := slimMediaProto(rawWire); len(raw) != 0 {
		t.Fatalf("slimMediaProto must return nil for ViewOnce, got %d bytes", len(raw))
	}
	if dl, _, _ := mediaPart(rawWire, "VO1"); dl != nil {
		t.Fatal("mediaPart must return nil for ViewOnce")
	}

	// Simulate whatsmeow's handler path where UnwrapRaw already unwrapped
	// Message=ImageMessage and set IsViewOnce=true.
	acctJID := types.NewJID("5511111111111", types.DefaultUserServer)
	cli := &whatsmeow.Client{Store: &store.Device{ID: &acctJID}}
	m := &Manager{
		store:     st,
		clients:   map[string]*whatsmeow.Client{"5511111111111": cli},
		loggedOut: map[string]time.Time{},
		log:       waLog.Noop,
	}
	m.startIngest(ctx)

	chatJID := types.NewJID("5511999998888", types.DefaultUserServer)
	m.handler(cli)(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chatJID, Sender: chatJID},
			ID:            "vo_msg_1",
			Timestamp:     time.Unix(1700000000, 0),
		},
		Message:    &waE2E.Message{ImageMessage: secretImg},
		RawMessage: rawWire,
		IsViewOnce: true,
	})
	cancel() // flush ingest queue
	time.Sleep(30 * time.Millisecond)

	msgs, err := st.ListMessages(context.Background(), "5511111111111", chatJID.String(), 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListMessages = %+v, err=%v", msgs, err)
	}
	if msgs[0].Body != viewOnceBody || msgs[0].MediaType != "view_once" {
		t.Fatalf("stored ViewOnce = %+v, want body=%q media_type=view_once", msgs[0], viewOnceBody)
	}
	storedProto, _, err := st.GetMedia(context.Background(), "5511111111111", "vo_msg_1")
	if err != nil {
		t.Fatalf("GetMedia: %v", err)
	}
	if len(storedProto) != 0 {
		t.Fatalf("ViewOnce must not persist media proto in DB, got %d bytes", len(storedProto))
	}
}

func TestOutboundMediaPersistedAndIsolatedPerAccount(t *testing.T) {
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
	cliA := &whatsmeow.Client{Store: &store.Device{ID: &acctAJID}}
	m := &Manager{
		store:     st,
		clients:   map[string]*whatsmeow.Client{"5511111111111": cliA},
		loggedOut: map[string]time.Time{},
		log:       waLog.Noop,
	}
	m.startIngest(ctx)

	chatJID := types.NewJID("5511988887777", types.DefaultUserServer)
	sentMsg := &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			URL:           proto.String("https://mmg.whatsapp.net/d/f/audio.enc"),
			DirectPath:    proto.String("/v/t62/audio.enc"),
			MediaKey:      []byte("0123456789abcdef0123456789abcdef"),
			FileSHA256:    []byte("audio-sha"),
			FileEncSHA256: []byte("audio-enc-sha"),
			FileLength:    proto.Uint64(1024),
			Mimetype:      proto.String("audio/ogg; codecs=opus"),
			PTT:           proto.Bool(true),
		},
	}
	m.recordOutboundMedia(cliA, chatJID, whatsmeow.SendResponse{ID: "out_audio_1", Timestamp: time.Unix(1700000100, 0)}, sentMsg, "", "audio")
	cancel()
	time.Sleep(30 * time.Millisecond)

	// Account A can retrieve the stored media metadata/keys.
	rawA, mtypeA, err := st.GetMedia(context.Background(), "5511111111111", "out_audio_1")
	if err != nil {
		t.Fatalf("GetMedia(acctA): %v", err)
	}
	if len(rawA) == 0 || mtypeA != "audio" {
		t.Fatalf("GetMedia(acctA) = (%d bytes, %q), want non-empty audio", len(rawA), mtypeA)
	}

	// NEGATIVE ISOLATION TEST: Account B cannot read Account A's media keys by message ID.
	rawB, mtypeB, err := st.GetMedia(context.Background(), "5522222222222", "out_audio_1")
	if err != nil {
		t.Fatalf("GetMedia(acctB): %v", err)
	}
	if len(rawB) != 0 || mtypeB != "" {
		t.Fatalf("cross-account media leak: Account B got (%d bytes, %q) for Account A's message", len(rawB), mtypeB)
	}
}
