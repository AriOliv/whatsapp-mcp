// Package wa wraps whatsmeow: it owns the multi-account client registry, the
// pairing flow, the event handler that feeds our message store, and the typed
// send/read helpers the MCP tools call.
package wa

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

// Manager owns the whatsmeow container and one client per linked account.
type Manager struct {
	container *sqlstore.Container
	store     *appstore.Store
	log       waLog.Logger

	mu      sync.RWMutex
	clients map[string]*whatsmeow.Client // key: account JID (user part @ server)
	def     string                       // default account key (stdio)
}

// New opens the whatsmeow sqlstore, sharing the app's *sql.DB, and sets the
// linked-device name shown in WhatsApp before any client is created.
func New(ctx context.Context, db *sql.DB, isPG bool, st *appstore.Store, deviceName string) (*Manager, error) {
	logger := waLog.Stdout("wa", "INFO", true)

	// The device name shown under "Linked devices" comes from DeviceProps.Os,
	// which is sent during pairing. Must be set before creating clients.
	if deviceName != "" {
		store.DeviceProps.Os = proto.String(deviceName)
	}

	dialect := "sqlite3" // dbutil dialect for a modernc "sqlite" *sql.DB
	if isPG {
		dialect = "postgres"
	}
	container := sqlstore.NewWithDB(db, dialect, logger)
	if err := container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("whatsmeow store upgrade: %w", err)
	}
	return &Manager{
		container: container,
		store:     st,
		log:       logger,
		clients:   map[string]*whatsmeow.Client{},
	}, nil
}

func accountKey(jid *types.JID) string {
	if jid == nil {
		return ""
	}
	return jid.User
}

// LoadAndConnect connects every already-paired device. Returns the number of
// accounts connected.
func (m *Manager) LoadAndConnect(ctx context.Context) (int, error) {
	devices, err := m.container.GetAllDevices(ctx)
	if err != nil {
		return 0, fmt.Errorf("get devices: %w", err)
	}
	n := 0
	for _, dev := range devices {
		cli := whatsmeow.NewClient(dev, m.log)
		m.register(cli)
		if err := cli.Connect(); err != nil {
			m.log.Errorf("connect %s: %v", accountKey(dev.ID), err)
			continue
		}
		n++
	}
	return n, nil
}

func (m *Manager) register(cli *whatsmeow.Client) {
	cli.AddEventHandler(m.handler(cli))
	key := accountKey(cli.Store.ID)
	m.mu.Lock()
	if key != "" {
		m.clients[key] = cli
		if m.def == "" {
			m.def = key
		}
	}
	m.mu.Unlock()
}

// PairInteractive creates a fresh device and prints a QR to the terminal for a
// local (stdio) pairing. Blocks until paired, timeout, or ctx cancel.
func (m *Manager) PairInteractive(ctx context.Context) error {
	dev := m.container.NewDevice()
	cli := whatsmeow.NewClient(dev, m.log)
	qrChan, err := cli.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("qr channel: %w", err)
	}
	if err := cli.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	for evt := range qrChan {
		switch evt.Event {
		case "code":
			fmt.Fprintln(os.Stderr, "\nScan this QR in WhatsApp → Linked devices:")
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stderr)
		case "success":
			cli.AddEventHandler(m.handler(cli))
			key := accountKey(cli.Store.ID)
			m.mu.Lock()
			m.clients[key] = cli
			if m.def == "" {
				m.def = key
			}
			m.mu.Unlock()
			fmt.Fprintf(os.Stderr, "\nPaired as %s\n", key)
			return nil
		case "timeout":
			return fmt.Errorf("pairing timed out")
		case "error":
			return fmt.Errorf("pairing error: %w", evt.Error)
		}
	}
	return fmt.Errorf("pairing channel closed")
}

func (m *Manager) clientFor(account string) (*whatsmeow.Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if account == "" {
		account = m.def
	}
	cli := m.clients[account]
	if cli == nil {
		return nil, fmt.Errorf("no connected WhatsApp account %q — pair one first", account)
	}
	return cli, nil
}

// handler persists incoming messages into our store and reacts to lifecycle.
func (m *Manager) handler(cli *whatsmeow.Client) func(any) {
	return func(evt any) {
		switch v := evt.(type) {
		case *events.Message:
			acct := accountKey(cli.Store.ID)
			body := messageText(v.Message)
			_ = m.store.SaveMessage(context.Background(), acct, appstore.Message{
				ID:        v.Info.ID,
				ChatJID:   v.Info.Chat.String(),
				SenderJID: v.Info.Sender.String(),
				FromMe:    v.Info.IsFromMe,
				TS:        v.Info.Timestamp.UnixMilli(),
				Body:      body,
				MediaType: mediaType(v.Message),
			})
		case *events.LoggedOut:
			key := accountKey(cli.Store.ID)
			m.mu.Lock()
			delete(m.clients, key)
			m.mu.Unlock()
			m.log.Warnf("account %s logged out", key)
		case *events.Connected:
			m.log.Infof("account %s connected", accountKey(cli.Store.ID))
		}
	}
}

// ---- send / read helpers -------------------------------------------------

// resolveJID turns a phone number or JID string into a user/group types.JID.
func resolveJID(s string) (types.JID, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "@") {
		return types.ParseJID(s)
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	if digits == "" {
		return types.JID{}, fmt.Errorf("invalid recipient %q", s)
	}
	return types.NewJID(digits, types.DefaultUserServer), nil
}

// SendText sends a plain text message; returns the sent message ID.
func (m *Manager) SendText(ctx context.Context, account, to, text string) (string, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return "", err
	}
	jid, err := resolveJID(to)
	if err != nil {
		return "", err
	}
	resp, err := cli.SendMessage(ctx, jid, &waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// SendMedia uploads and sends an image/video/document/audio. `data` is base64 or
// an http(s) URL; kind is one of image|video|document|audio.
func (m *Manager) SendMedia(ctx context.Context, account, to, kind, data, caption, mimeType, fileName string) (string, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return "", err
	}
	jid, err := resolveJID(to)
	if err != nil {
		return "", err
	}
	raw, err := loadBytes(ctx, data)
	if err != nil {
		return "", err
	}
	var mt whatsmeow.MediaType
	switch kind {
	case "image":
		mt = whatsmeow.MediaImage
	case "video":
		mt = whatsmeow.MediaVideo
	case "audio":
		mt = whatsmeow.MediaAudio
	case "document":
		mt = whatsmeow.MediaDocument
	default:
		return "", fmt.Errorf("unknown media kind %q", kind)
	}
	up, err := cli.Upload(ctx, raw, mt)
	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}
	msg := &waE2E.Message{}
	switch kind {
	case "image":
		msg.ImageMessage = &waE2E.ImageMessage{
			Caption: strPtrOrNil(caption), Mimetype: strPtrOrNil(mimeType),
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
		}
	case "video":
		msg.VideoMessage = &waE2E.VideoMessage{
			Caption: strPtrOrNil(caption), Mimetype: strPtrOrNil(mimeType),
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
		}
	case "audio":
		msg.AudioMessage = &waE2E.AudioMessage{
			Mimetype: proto.String(orDefault(mimeType, "audio/ogg; codecs=opus")), PTT: proto.Bool(true),
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
		}
	case "document":
		msg.DocumentMessage = &waE2E.DocumentMessage{
			Caption: strPtrOrNil(caption), Mimetype: strPtrOrNil(mimeType), FileName: strPtrOrNil(fileName),
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
		}
	}
	resp, err := cli.SendMessage(ctx, jid, msg)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// React reacts to a message (empty emoji removes the reaction).
func (m *Manager) React(ctx context.Context, account, chat, sender, msgID, emoji string) (string, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return "", err
	}
	chatJID, err := resolveJID(chat)
	if err != nil {
		return "", err
	}
	senderJID := chatJID
	if sender != "" {
		if senderJID, err = resolveJID(sender); err != nil {
			return "", err
		}
	}
	msg := cli.BuildReaction(chatJID, senderJID, types.MessageID(msgID), emoji)
	resp, err := cli.SendMessage(ctx, chatJID, msg)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// Contacts returns the account's stored contacts.
func (m *Manager) Contacts(ctx context.Context, account string) (map[types.JID]types.ContactInfo, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return nil, err
	}
	return cli.Store.Contacts.GetAllContacts(ctx)
}

// CheckNumbers reports which numbers are on WhatsApp.
func (m *Manager) CheckNumbers(ctx context.Context, account string, numbers []string) ([]types.IsOnWhatsAppResponse, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return nil, err
	}
	return cli.IsOnWhatsApp(ctx, numbers)
}

// Groups returns all joined groups.
func (m *Manager) Groups(ctx context.Context, account string) ([]*types.GroupInfo, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return nil, err
	}
	return cli.GetJoinedGroups(ctx)
}

// GroupInfo returns info for one group.
func (m *Manager) GroupInfo(ctx context.Context, account, groupJID string) (*types.GroupInfo, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return nil, err
	}
	jid, err := types.ParseJID(groupJID)
	if err != nil {
		return nil, err
	}
	return cli.GetGroupInfo(ctx, jid)
}

// CreateGroup creates a group with the given participants (numbers or JIDs).
func (m *Manager) CreateGroup(ctx context.Context, account, name string, participants []string) (*types.GroupInfo, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return nil, err
	}
	jids, err := resolveJIDs(participants)
	if err != nil {
		return nil, err
	}
	return cli.CreateGroup(ctx, whatsmeow.ReqCreateGroup{Name: name, Participants: jids})
}

// InviteLink returns a group's invite link.
func (m *Manager) InviteLink(ctx context.Context, account, groupJID string, reset bool) (string, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return "", err
	}
	jid, err := types.ParseJID(groupJID)
	if err != nil {
		return "", err
	}
	return cli.GetGroupInviteLink(ctx, jid, reset)
}

// UpdateParticipants add/remove/promote/demote group members.
func (m *Manager) UpdateParticipants(ctx context.Context, account, groupJID, action string, participants []string) error {
	cli, err := m.clientFor(account)
	if err != nil {
		return err
	}
	jid, err := types.ParseJID(groupJID)
	if err != nil {
		return err
	}
	jids, err := resolveJIDs(participants)
	if err != nil {
		return err
	}
	var change whatsmeow.ParticipantChange
	switch action {
	case "add":
		change = whatsmeow.ParticipantChangeAdd
	case "remove":
		change = whatsmeow.ParticipantChangeRemove
	case "promote":
		change = whatsmeow.ParticipantChangePromote
	case "demote":
		change = whatsmeow.ParticipantChangeDemote
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	_, err = cli.UpdateGroupParticipants(ctx, jid, jids, change)
	return err
}

// SetGroupName changes a group's subject.
func (m *Manager) SetGroupName(ctx context.Context, account, groupJID, name string) error {
	cli, err := m.clientFor(account)
	if err != nil {
		return err
	}
	jid, err := types.ParseJID(groupJID)
	if err != nil {
		return err
	}
	return cli.SetGroupName(ctx, jid, name)
}

// LeaveGroup leaves a group.
func (m *Manager) LeaveGroup(ctx context.Context, account, groupJID string) error {
	cli, err := m.clientFor(account)
	if err != nil {
		return err
	}
	jid, err := types.ParseJID(groupJID)
	if err != nil {
		return err
	}
	return cli.LeaveGroup(ctx, jid)
}

// ListChats / ListMessages delegate to our store.
func (m *Manager) ListChats(ctx context.Context, account string, limit int) ([]appstore.Chat, error) {
	return m.store.ListChats(ctx, m.acct(account), limit)
}

func (m *Manager) ListMessages(ctx context.Context, account, chatJID string, limit int) ([]appstore.Message, error) {
	return m.store.ListMessages(ctx, m.acct(account), chatJID, limit)
}

func (m *Manager) acct(account string) string {
	if account != "" {
		return account
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	// store keys chats/messages by the full account JID string; resolve via client
	if cli := m.clients[m.def]; cli != nil && cli.Store.ID != nil {
		return cli.Store.ID.String()
	}
	return m.def
}

// ---- small helpers -------------------------------------------------------

func messageText(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	if m.Conversation != nil {
		return m.GetConversation()
	}
	if et := m.GetExtendedTextMessage(); et != nil {
		return et.GetText()
	}
	if im := m.GetImageMessage(); im != nil {
		return im.GetCaption()
	}
	if vm := m.GetVideoMessage(); vm != nil {
		return vm.GetCaption()
	}
	return ""
}

func mediaType(m *waE2E.Message) string {
	switch {
	case m == nil:
		return ""
	case m.GetImageMessage() != nil:
		return "image"
	case m.GetVideoMessage() != nil:
		return "video"
	case m.GetAudioMessage() != nil:
		return "audio"
	case m.GetDocumentMessage() != nil:
		return "document"
	case m.GetStickerMessage() != nil:
		return "sticker"
	default:
		return ""
	}
}

func resolveJIDs(items []string) ([]types.JID, error) {
	out := make([]types.JID, 0, len(items))
	for _, it := range items {
		j, err := resolveJID(it)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

func loadBytes(ctx context.Context, data string) ([]byte, error) {
	if strings.HasPrefix(data, "http://") || strings.HasPrefix(data, "https://") {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, data, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	}
	if i := strings.Index(data, ","); strings.HasPrefix(data, "data:") && i > 0 {
		data = data[i+1:]
	}
	return base64.StdEncoding.DecodeString(data)
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return proto.String(s)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

var _ = time.Now // reserved for presence/mark-read helpers (phase 2b)
