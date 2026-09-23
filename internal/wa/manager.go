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
	waCompanionReg "go.mau.fi/whatsmeow/proto/waCompanionReg"
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
	flows   map[string]*PairFlow         // key: OAuth flow id (HTTP pairing)

	groupMu    sync.Mutex
	groupCache map[string]cachedNames // account JID -> joined-group subjects (ListChats)
	nlMu       sync.Mutex
	nlCache    map[string]cachedNames // account JID -> subscribed-newsletter names (ListChats)
}

// New opens the whatsmeow sqlstore, sharing the app's *sql.DB, and sets the
// linked-device name shown in WhatsApp before any client is created.
func New(ctx context.Context, db *sql.DB, isPG bool, st *appstore.Store, deviceName string) (*Manager, error) {
	logger := waLog.Stdout("wa", "INFO", true)

	// The device name shown under "Linked devices" comes from DeviceProps.Os,
	// which is sent during pairing. Must be set before creating clients.
	if deviceName != "" {
		store.DeviceProps.Os = proto.String(deviceName)
		// PlatformType must be a recognized value or WhatsApp shows "Other Device"
		// instead of our Os string. CHROME mirrors what Baileys/Evolution do.
		store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_CHROME.Enum()
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
		container:  container,
		store:      st,
		log:        logger,
		clients:    map[string]*whatsmeow.Client{},
		flows:      map[string]*PairFlow{},
		groupCache: map[string]cachedNames{},
		nlCache:    map[string]cachedNames{},
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

// PairWithCode pairs via an 8-digit phone code (WhatsApp → Linked devices →
// "Link with phone number instead"). onCode is called once with the code so the
// caller can surface it. Blocks until paired, timeout, or ctx cancel. `number`
// is an international number (any punctuation is stripped).
func (m *Manager) PairWithCode(ctx context.Context, number string, onCode func(string)) error {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, number)
	if digits == "" {
		return fmt.Errorf("invalid phone number %q", number)
	}
	dev := m.container.NewDevice()
	cli := whatsmeow.NewClient(dev, m.log)
	qrChan, err := cli.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("qr channel: %w", err)
	}
	if err := cli.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	requested := false
	for evt := range qrChan {
		switch evt.Event {
		case "code":
			if !requested {
				requested = true
				code, err := cli.PairPhone(ctx, digits, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
				if err != nil {
					return fmt.Errorf("pair phone: %w", err)
				}
				if onCode != nil {
					onCode(code)
				}
			}
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
			return fmt.Errorf("pairing timed out (code expired) — try again")
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

// HasDevice reports whether the given subject (phone) has a live client. Used by
// the OAuth store so refresh tokens stay valid only while the device is paired,
// and (crucially) so refresh survives pod restarts once LoadAndConnect has
// reconnected the device.
func (m *Manager) HasDevice(sub string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clients[sub] != nil
}

// handler persists incoming messages into our store and reacts to lifecycle.
func (m *Manager) handler(cli *whatsmeow.Client) func(any) {
	return func(evt any) {
		switch v := evt.(type) {
		case *events.Message:
			acct := accountKey(cli.Store.ID)
			body := messageText(v.Message)
			mt := mediaType(v.Message)
			// Persist the message proto for media so it can be downloaded later
			// (it carries MediaKey/DirectPath/URL — the raw bytes stay on the CDN).
			var mediaRaw []byte
			if mt != "" {
				if b, err := proto.Marshal(v.Message); err == nil {
					mediaRaw = b
				}
			}
			_ = m.store.SaveMessage(context.Background(), acct, appstore.Message{
				ID:         v.Info.ID,
				ChatJID:    v.Info.Chat.String(),
				SenderJID:  v.Info.Sender.String(),
				FromMe:     v.Info.IsFromMe,
				TS:         v.Info.Timestamp.UnixMilli(),
				Body:       body,
				MediaType:  mt,
				MediaProto: mediaRaw,
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

// SendSticker sends a webp sticker (URL or base64).
func (m *Manager) SendSticker(ctx context.Context, account, to, data string) (string, error) {
	cli, jid, raw, err := m.prep(ctx, account, to, data)
	if err != nil {
		return "", err
	}
	up, err := cli.Upload(ctx, raw, whatsmeow.MediaImage)
	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}
	msg := &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
		Mimetype: proto.String("image/webp"),
		URL:      &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
		FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
	}}
	resp, err := cli.SendMessage(ctx, jid, msg)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// SendLocation sends a location pin.
func (m *Manager) SendLocation(ctx context.Context, account, to string, lat, lon float64, name, address string) (string, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return "", err
	}
	jid, err := resolveJID(to)
	if err != nil {
		return "", err
	}
	resp, err := cli.SendMessage(ctx, jid, &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
		DegreesLatitude: proto.Float64(lat), DegreesLongitude: proto.Float64(lon),
		Name: strPtrOrNil(name), Address: strPtrOrNil(address),
	}})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// SendContact sends a single contact card.
func (m *Manager) SendContact(ctx context.Context, account, to, fullName, phone string) (string, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return "", err
	}
	jid, err := resolveJID(to)
	if err != nil {
		return "", err
	}
	waid := onlyDigits(phone)
	vcard := fmt.Sprintf("BEGIN:VCARD\nVERSION:3.0\nFN:%s\nTEL;type=CELL;type=VOICE;waid=%s:%s\nEND:VCARD", fullName, waid, phone)
	resp, err := cli.SendMessage(ctx, jid, &waE2E.Message{ContactMessage: &waE2E.ContactMessage{
		DisplayName: proto.String(fullName), Vcard: proto.String(vcard),
	}})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// SendPoll sends a poll.
func (m *Manager) SendPoll(ctx context.Context, account, to, name string, options []string, selectable int) (string, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return "", err
	}
	jid, err := resolveJID(to)
	if err != nil {
		return "", err
	}
	if selectable <= 0 {
		selectable = 1
	}
	resp, err := cli.SendMessage(ctx, jid, cli.BuildPollCreation(name, options, selectable))
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// DeleteMessage revokes (deletes for everyone) one of our own messages.
func (m *Manager) DeleteMessage(ctx context.Context, account, chat, msgID string) (string, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return "", err
	}
	chatJID, err := resolveJID(chat)
	if err != nil {
		return "", err
	}
	own := types.EmptyJID
	if cli.Store.ID != nil {
		own = *cli.Store.ID
	}
	resp, err := cli.SendMessage(ctx, chatJID, cli.BuildRevoke(chatJID, own, types.MessageID(msgID)))
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// EditMessage edits the text of one of our own messages.
func (m *Manager) EditMessage(ctx context.Context, account, chat, msgID, newText string) (string, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return "", err
	}
	chatJID, err := resolveJID(chat)
	if err != nil {
		return "", err
	}
	newMsg := &waE2E.Message{Conversation: proto.String(newText)}
	resp, err := cli.SendMessage(ctx, chatJID, cli.BuildEdit(chatJID, types.MessageID(msgID), newMsg))
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// SetPresence sets global presence: available | unavailable.
func (m *Manager) SetPresence(ctx context.Context, account, presence string) error {
	cli, err := m.clientFor(account)
	if err != nil {
		return err
	}
	switch presence {
	case "available":
		return cli.SendPresence(ctx, types.PresenceAvailable)
	case "unavailable":
		return cli.SendPresence(ctx, types.PresenceUnavailable)
	default:
		return fmt.Errorf("presence must be available|unavailable")
	}
}

// ChatPresence sends a typing/recording indicator to a chat.
func (m *Manager) ChatPresence(ctx context.Context, account, to, state string) error {
	cli, err := m.clientFor(account)
	if err != nil {
		return err
	}
	jid, err := resolveJID(to)
	if err != nil {
		return err
	}
	switch state {
	case "composing":
		return cli.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	case "recording":
		return cli.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaAudio)
	case "paused":
		return cli.SendChatPresence(ctx, jid, types.ChatPresencePaused, types.ChatPresenceMediaText)
	default:
		return fmt.Errorf("state must be composing|recording|paused")
	}
}

// MarkRead marks messages as read in a chat.
func (m *Manager) MarkRead(ctx context.Context, account, chat, sender string, ids []string) error {
	cli, err := m.clientFor(account)
	if err != nil {
		return err
	}
	chatJID, err := resolveJID(chat)
	if err != nil {
		return err
	}
	senderJID := chatJID
	if sender != "" {
		if senderJID, err = resolveJID(sender); err != nil {
			return err
		}
	}
	msgIDs := make([]types.MessageID, 0, len(ids))
	for _, s := range ids {
		msgIDs = append(msgIDs, types.MessageID(s))
	}
	return cli.MarkRead(ctx, msgIDs, time.Now(), chatJID, senderJID)
}

// prep resolves the client + JID and loads media bytes in one shot.
func (m *Manager) prep(ctx context.Context, account, to, data string) (*whatsmeow.Client, types.JID, []byte, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return nil, types.JID{}, nil, err
	}
	jid, err := resolveJID(to)
	if err != nil {
		return nil, types.JID{}, nil, err
	}
	raw, err := loadBytes(ctx, data)
	if err != nil {
		return nil, types.JID{}, nil, err
	}
	return cli, jid, raw, nil
}

func onlyDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// GroupParticipants returns a group's participant list.
func (m *Manager) GroupParticipants(ctx context.Context, account, groupJID string) ([]types.GroupParticipant, error) {
	info, err := m.GroupInfo(ctx, account, groupJID)
	if err != nil {
		return nil, err
	}
	return info.Participants, nil
}

// FetchProfile returns basic user info for a number.
func (m *Manager) FetchProfile(ctx context.Context, account, number string) (map[types.JID]types.UserInfo, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return nil, err
	}
	jid, err := resolveJID(number)
	if err != nil {
		return nil, err
	}
	return cli.GetUserInfo(ctx, []types.JID{jid})
}

// FetchBusinessProfile returns a number's WhatsApp Business profile.
func (m *Manager) FetchBusinessProfile(ctx context.Context, account, number string) (*types.BusinessProfile, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return nil, err
	}
	jid, err := resolveJID(number)
	if err != nil {
		return nil, err
	}
	return cli.GetBusinessProfile(ctx, jid)
}

// ProfilePictureURL returns a contact's profile picture URL.
func (m *Manager) ProfilePictureURL(ctx context.Context, account, number string) (string, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return "", err
	}
	jid, err := resolveJID(number)
	if err != nil {
		return "", err
	}
	info, err := cli.GetProfilePictureInfo(ctx, jid, nil)
	if err != nil {
		return "", err
	}
	if info == nil {
		return "", nil
	}
	return info.URL, nil
}

// GetPrivacy returns the account's privacy settings.
func (m *Manager) GetPrivacy(ctx context.Context, account string) (types.PrivacySettings, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return types.PrivacySettings{}, err
	}
	return cli.GetPrivacySettings(ctx), nil
}

// UpdateProfileStatus sets the account's "about" text.
func (m *Manager) UpdateProfileStatus(ctx context.Context, account, status string) error {
	cli, err := m.clientFor(account)
	if err != nil {
		return err
	}
	return cli.SetStatusMessage(ctx, types.SetStatusInput{Text: proto.String(status)})
}

// BlockContact blocks or unblocks a number.
func (m *Manager) BlockContact(ctx context.Context, account, number, action string) error {
	cli, err := m.clientFor(account)
	if err != nil {
		return err
	}
	jid, err := resolveJID(number)
	if err != nil {
		return err
	}
	var act events.BlocklistChangeAction
	switch action {
	case "block":
		act = events.BlocklistChangeActionBlock
	case "unblock":
		act = events.BlocklistChangeActionUnblock
	default:
		return fmt.Errorf("action must be block|unblock")
	}
	_, err = cli.UpdateBlocklist(ctx, jid, act)
	return err
}

// InstanceState reports connection/login state of an account.
func (m *Manager) InstanceState(ctx context.Context, account string) (map[string]any, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return nil, err
	}
	jid := ""
	if cli.Store.ID != nil {
		jid = cli.Store.ID.String()
	}
	return map[string]any{"jid": jid, "connected": cli.IsConnected(), "loggedIn": cli.IsLoggedIn()}, nil
}

// Logout unlinks the account's device.
func (m *Manager) Logout(ctx context.Context, account string) error {
	cli, err := m.clientFor(account)
	if err != nil {
		return err
	}
	if err := cli.Logout(ctx); err != nil {
		return err
	}
	key := accountKey(cli.Store.ID)
	m.mu.Lock()
	delete(m.clients, key)
	if m.def == key {
		m.def = ""
	}
	m.mu.Unlock()
	return nil
}

// ListDevices lists the linked-account JIDs known to the store.
func (m *Manager) ListDevices(ctx context.Context) ([]string, error) {
	devices, err := m.container.GetAllDevices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(devices))
	for _, d := range devices {
		if d.ID != nil {
			out = append(out, d.ID.String())
		}
	}
	return out, nil
}

// ListChats / ListMessages delegate to our store. ListChats additionally
// resolves a display name for each chat — the store only records JIDs — using
// joined-group subjects (cached) for groups and the contact store for
// individuals.
func (m *Manager) ListChats(ctx context.Context, account string, limit int) ([]appstore.Chat, error) {
	chats, err := m.store.ListChats(ctx, m.acct(account), limit)
	if err != nil {
		return nil, err
	}
	m.fillChatNames(ctx, account, chats)
	return chats, nil
}

func (m *Manager) ListMessages(ctx context.Context, account, chatJID string, limit int) ([]appstore.Message, error) {
	return m.store.ListMessages(ctx, m.acct(account), chatJID, limit)
}

// maxMediaBytes caps a single media download so the base64 result stays within
// what the transport can carry.
const maxMediaBytes = 20 << 20 // 20 MiB

// DownloadMedia fetches the media bytes of a stored message (image, audio/voice,
// video, document, sticker) by message ID, using the message proto we persisted
// at receive/send time. Returns the bytes, mime type and a suggested filename.
// Only messages stored after media support was enabled carry the proto.
func (m *Manager) DownloadMedia(ctx context.Context, account, msgID string) ([]byte, string, string, error) {
	cli, err := m.clientFor(account)
	if err != nil {
		return nil, "", "", err
	}
	raw, _, err := m.store.GetMedia(ctx, m.acct(account), msgID)
	if err != nil {
		return nil, "", "", err
	}
	if len(raw) == 0 {
		return nil, "", "", fmt.Errorf("no downloadable media stored for message %q — only media received or sent after media support was enabled can be downloaded", msgID)
	}
	var msg waE2E.Message
	if err := proto.Unmarshal(raw, &msg); err != nil {
		return nil, "", "", fmt.Errorf("decode stored media: %w", err)
	}
	dl, mime, name := mediaPart(&msg, msgID)
	if dl == nil {
		return nil, "", "", fmt.Errorf("message %q carries no downloadable media", msgID)
	}
	data, err := cli.Download(ctx, dl)
	if err != nil {
		return nil, "", "", fmt.Errorf("download: %w", err)
	}
	if len(data) > maxMediaBytes {
		return nil, "", "", fmt.Errorf("media is %d bytes, over the %d limit", len(data), maxMediaBytes)
	}
	return data, mime, name, nil
}

// mediaPart returns the downloadable media part of a message plus its mime type
// and a suggested filename (msgID + extension derived from the mime type).
func mediaPart(msg *waE2E.Message, msgID string) (whatsmeow.DownloadableMessage, string, string) {
	switch {
	case msg.GetImageMessage() != nil:
		p := msg.GetImageMessage()
		return p, p.GetMimetype(), mediaFilename(msgID, p.GetMimetype(), "jpg")
	case msg.GetVideoMessage() != nil:
		p := msg.GetVideoMessage()
		return p, p.GetMimetype(), mediaFilename(msgID, p.GetMimetype(), "mp4")
	case msg.GetAudioMessage() != nil:
		p := msg.GetAudioMessage()
		return p, p.GetMimetype(), mediaFilename(msgID, p.GetMimetype(), "ogg")
	case msg.GetDocumentMessage() != nil:
		p := msg.GetDocumentMessage()
		name := p.GetFileName()
		if name == "" {
			name = mediaFilename(msgID, p.GetMimetype(), "bin")
		}
		return p, p.GetMimetype(), name
	case msg.GetStickerMessage() != nil:
		p := msg.GetStickerMessage()
		return p, p.GetMimetype(), mediaFilename(msgID, p.GetMimetype(), "webp")
	default:
		return nil, "", ""
	}
}

// mediaFilename builds "<msgID>.<ext>" from a mime type, falling back to def.
func mediaFilename(msgID, mime, def string) string {
	ext := def
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = mime[:i]
	}
	if j := strings.IndexByte(mime, '/'); j >= 0 {
		if sub := strings.TrimSpace(mime[j+1:]); sub != "" {
			switch sub {
			case "jpeg":
				ext = "jpg"
			case "mpeg":
				ext = "mp3"
			default:
				ext = sub
			}
		}
	}
	return msgID + "." + ext
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

// groupNameTTL bounds how often ListChats refreshes joined-group subjects and
// subscribed-newsletter names: each is a network round-trip that returns all.
const groupNameTTL = 5 * time.Minute

type cachedNames struct {
	at    time.Time
	names map[string]string // JID string -> display name
}

// fillChatNames resolves a display name for every chat that has none. It is
// best-effort: any lookup failure leaves that chat's name empty rather than
// failing the whole listing. Groups use joined-group subjects (cached);
// individuals use the whatsmeow contact store; status/newsletter are special.
func (m *Manager) fillChatNames(ctx context.Context, account string, chats []appstore.Chat) {
	needGroup, needNewsletter, needOther := false, false, false
	for i := range chats {
		if chats[i].Name != "" {
			continue
		}
		switch {
		case strings.HasSuffix(chats[i].JID, "@g.us"):
			needGroup = true
		case strings.HasSuffix(chats[i].JID, "@newsletter"):
			needNewsletter = true
		case chats[i].JID == "status@broadcast":
			// handled without a lookup
		default:
			needOther = true
		}
	}
	if !needGroup && !needNewsletter && !needOther {
		return
	}
	cli, err := m.clientFor(account)
	if err != nil {
		return // not connected: leave names empty rather than error the listing
	}
	var groups, newsletters map[string]string
	if needGroup {
		groups = m.groupNames(ctx, account, cli)
	}
	if needNewsletter {
		newsletters = m.newsletterNames(ctx, account, cli)
	}
	for i := range chats {
		c := &chats[i]
		if c.Name != "" {
			continue
		}
		switch {
		case strings.HasSuffix(c.JID, "@g.us"):
			c.Name = groups[c.JID]
		case strings.HasSuffix(c.JID, "@newsletter"):
			c.Name = newsletters[c.JID]
		case c.JID == "status@broadcast":
			c.Name = "Status"
		default:
			jid, err := types.ParseJID(c.JID)
			if err != nil {
				continue
			}
			if info, err := cli.Store.Contacts.GetContact(ctx, jid); err == nil {
				c.Name = bestContactName(info)
			}
		}
	}
}

// bestContactName picks the most human name from a whatsmeow contact, following
// the precedence WhatsApp itself uses.
func bestContactName(c types.ContactInfo) string {
	switch {
	case c.FullName != "":
		return c.FullName
	case c.PushName != "":
		return c.PushName
	case c.BusinessName != "":
		return c.BusinessName
	default:
		return c.RedactedPhone
	}
}

// groupNames returns a JID->subject map for the account's joined groups, cached
// for groupNameTTL so find_chats doesn't pay a GetJoinedGroups round-trip on
// every call. On refresh failure it falls back to any (stale) cached map.
func (m *Manager) groupNames(ctx context.Context, account string, cli *whatsmeow.Client) map[string]string {
	key := m.acct(account)

	m.groupMu.Lock()
	if cg, ok := m.groupCache[key]; ok && time.Since(cg.at) < groupNameTTL {
		m.groupMu.Unlock()
		return cg.names
	}
	m.groupMu.Unlock()

	groups, err := cli.GetJoinedGroups(ctx)
	if err != nil {
		m.groupMu.Lock()
		defer m.groupMu.Unlock()
		if cg, ok := m.groupCache[key]; ok {
			return cg.names // stale is better than nothing
		}
		return map[string]string{}
	}
	names := make(map[string]string, len(groups))
	for _, g := range groups {
		names[g.JID.String()] = g.Name
	}
	m.groupMu.Lock()
	m.groupCache[key] = cachedNames{at: time.Now(), names: names}
	m.groupMu.Unlock()
	return names
}

// newsletterNames returns a JID->name map for the account's subscribed
// newsletters (channels), cached for groupNameTTL so find_chats doesn't pay a
// GetSubscribedNewsletters round-trip on every call. Falls back to any stale
// cache on failure.
func (m *Manager) newsletterNames(ctx context.Context, account string, cli *whatsmeow.Client) map[string]string {
	key := m.acct(account)

	m.nlMu.Lock()
	if cn, ok := m.nlCache[key]; ok && time.Since(cn.at) < groupNameTTL {
		m.nlMu.Unlock()
		return cn.names
	}
	m.nlMu.Unlock()

	list, err := cli.GetSubscribedNewsletters(ctx)
	if err != nil {
		m.nlMu.Lock()
		defer m.nlMu.Unlock()
		if cn, ok := m.nlCache[key]; ok {
			return cn.names // stale is better than nothing
		}
		return map[string]string{}
	}
	names := make(map[string]string, len(list))
	for _, nl := range list {
		if nl != nil {
			names[nl.ID.String()] = nl.ThreadMeta.Name.Text
		}
	}
	m.nlMu.Lock()
	m.nlCache[key] = cachedNames{at: time.Now(), names: names}
	m.nlMu.Unlock()
	return names
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

// DefaultNumber returns the default (stdio) account's phone number, or "".
func (m *Manager) DefaultNumber() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if cli := m.clients[m.def]; cli != nil && cli.Store.ID != nil {
		return cli.Store.ID.User
	}
	return ""
}

// WaitReady blocks until the default client is logged in and connected, or the
// timeout elapses.
func (m *Manager) WaitReady(ctx context.Context, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		m.mu.RLock()
		cli := m.clients[m.def]
		m.mu.RUnlock()
		if cli != nil && cli.IsConnected() && cli.IsLoggedIn() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	return false
}
