package wa

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

const (
	// historyQueueSize bounds how many downloaded HistorySync blobs may wait on
	// the background writer before new blobs are dropped and logged.
	historyQueueSize = 64
	// historyBatchSize bounds how many messages are upserted per SQL transaction
	// so no single transaction holds a database connection open for long.
	historyBatchSize = 200
	// historyMaxPerChat caps how many messages from a single conversation are
	// ingested per HistorySync blob (newest first).
	historyMaxPerChat = 500
	// historyWriteTimeout bounds a single batch transaction.
	historyWriteTimeout = 30 * time.Second
	// maxOnDemandHistoryCount is WhatsApp's recommended cap per on-demand
	// history request sent to the user's primary phone.
	maxOnDemandHistoryCount = 50
)

type historyJob struct {
	account string
	cli     *whatsmeow.Client
	data    *waHistorySync.HistorySync
}

func isHistorySyncDisabled(envVal string) bool {
	switch strings.ToLower(strings.TrimSpace(envVal)) {
	case "off", "false", "0", "no":
		return true
	default:
		return false
	}
}

// startHistorySync launches the background worker that processes HistorySync
// blobs off the whatsmeow event handler path.
func (m *Manager) startHistorySync(ctx context.Context) {
	m.history = make(chan historyJob, historyQueueSize)
	go func() {
		for {
			select {
			case job := <-m.history:
				m.processHistorySync(job)
			case <-ctx.Done():
				for {
					select {
					case job := <-m.history:
						m.processHistorySync(job)
					default:
						return
					}
				}
			}
		}
	}()
}

// enqueueHistorySync hands a HistorySync payload to the background worker. It
// never blocks: if WA_HISTORY_SYNC=off is set at boot it is a no-op, and if the
// bounded queue is full the blob is dropped and logged with throttling.
func (m *Manager) enqueueHistorySync(account string, cli *whatsmeow.Client, data *waHistorySync.HistorySync) {
	if m.historySyncOff || data == nil || account == "" || m.history == nil {
		return
	}
	select {
	case m.history <- historyJob{account: account, cli: cli, data: data}:
	default:
		n := m.historyDropped.Add(1)
		m.warnThrottled(&m.historyDropLog, "history sync queue full; %d blob(s) dropped so far", n)
	}
}

func (m *Manager) processHistorySync(job historyJob) {
	if m.store == nil || job.data == nil || job.account == "" {
		return
	}
	pushNames := make(map[string]string, len(job.data.GetPushnames()))
	for _, pn := range job.data.GetPushnames() {
		if id, name := strings.TrimSpace(pn.GetID()), strings.TrimSpace(pn.GetPushname()); id != "" && name != "" {
			pushNames[id] = name
		}
	}

	batch := make([]appstore.Message, 0, historyBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), historyWriteTimeout)
		defer cancel()
		if err := m.store.SaveHistoryBatch(ctx, job.account, batch); err != nil {
			n := m.historyFailed.Add(1)
			m.warnThrottled(&m.historyFailLog, "storing history sync batch is failing (%d so far), last error: %v", n, err)
		}
		batch = batch[:0]
	}

	for _, conv := range job.data.GetConversations() {
		rawID := strings.TrimSpace(conv.GetID())
		if rawID == "" {
			continue
		}
		chatJID, err := types.ParseJID(rawID)
		if err != nil {
			continue
		}
		chatName := strings.TrimSpace(conv.GetName())
		if chatName == "" {
			chatName = strings.TrimSpace(conv.GetDisplayName())
		}
		if chatName == "" {
			chatName = pushNames[chatJID.String()]
		}

		items := conv.GetMessages()
		if len(items) > historyMaxPerChat {
			sorted := make([]*waHistorySync.HistorySyncMsg, len(items))
			copy(sorted, items)
			sort.Slice(sorted, func(i, j int) bool {
				return sorted[i].GetMessage().GetMessageTimestamp() > sorted[j].GetMessage().GetMessageTimestamp()
			})
			items = sorted[:historyMaxPerChat]
		}

		for _, hm := range items {
			msg, ok := extractHistoryMessage(job.cli, chatJID, chatName, hm.GetMessage())
			if !ok {
				continue
			}
			batch = append(batch, msg)
			if len(batch) >= historyBatchSize {
				flush()
			}
		}
	}
	flush()
}

func extractHistoryMessage(cli *whatsmeow.Client, chatJID types.JID, chatName string, webMsg *waWeb.WebMessageInfo) (appstore.Message, bool) {
	if webMsg == nil || webMsg.GetKey() == nil || webMsg.GetMessage() == nil {
		return appstore.Message{}, false
	}
	evt, err := parseWebMsg(cli, chatJID, webMsg)
	if err != nil || evt == nil || evt.Info.ID == "" {
		return appstore.Message{}, false
	}

	var (
		body     string
		mt       string
		mediaRaw []byte
	)
	if evt.IsViewOnce || isViewOnceMessage(evt.RawMessage) || isViewOnceMessage(evt.Message) {
		body = viewOnceBody
		mt = "view_once"
	} else {
		body = messageText(evt.Message)
		mt = mediaType(evt.Message)
		if mt != "" {
			mediaRaw, _ = slimMediaProto(evt.Message)
		}
	}
	if body == "" && mt == "" {
		return appstore.Message{}, false
	}

	resolvedChatName := chatName
	if resolvedChatName == "" && !evt.Info.IsFromMe && !evt.Info.IsGroup && chatJID.Server != types.NewsletterServer && chatJID.String() != "status@broadcast" {
		resolvedChatName = strings.TrimSpace(evt.Info.PushName)
	}

	return appstore.Message{
		ID:         evt.Info.ID,
		ChatJID:    chatJID.String(),
		SenderJID:  evt.Info.Sender.String(),
		FromMe:     evt.Info.IsFromMe,
		TS:         evt.Info.Timestamp.UnixMilli(),
		Body:       body,
		MediaType:  mt,
		MediaProto: mediaRaw,
		ChatName:   resolvedChatName,
	}, true
}

func parseWebMsg(cli *whatsmeow.Client, chatJID types.JID, webMsg *waWeb.WebMessageInfo) (*events.Message, error) {
	if cli != nil && cli.Store != nil && cli.Store.ID != nil {
		if evt, err := cli.ParseWebMessage(chatJID, webMsg); err == nil {
			return evt, nil
		}
	}
	key := webMsg.GetKey()
	fromMe := key.GetFromMe()
	var sender types.JID
	switch {
	case fromMe && cli != nil && cli.Store != nil && cli.Store.ID != nil:
		sender = cli.Store.ID.ToNonAD()
	case !fromMe && (chatJID.Server == types.DefaultUserServer || chatJID.Server == types.HiddenUserServer || chatJID.Server == types.NewsletterServer):
		sender = chatJID
	case webMsg.GetParticipant() != "":
		sender, _ = types.ParseJID(webMsg.GetParticipant())
	case key.GetParticipant() != "":
		sender, _ = types.ParseJID(key.GetParticipant())
	default:
		sender = chatJID
	}
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     chatJID,
				Sender:   sender,
				IsFromMe: fromMe,
				IsGroup:  chatJID.Server == types.GroupServer,
			},
			ID:        key.GetID(),
			PushName:  webMsg.GetPushName(),
			Timestamp: time.Unix(int64(webMsg.GetMessageTimestamp()), 0),
		},
		RawMessage:   webMsg.GetMessage(),
		SourceWebMsg: webMsg,
	}
	evt.UnwrapRaw()
	if evt.Message.GetProtocolMessage().GetType() == waE2E.ProtocolMessage_MESSAGE_EDIT {
		evt.Info.ID = evt.Message.GetProtocolMessage().GetKey().GetID()
		evt.Message = evt.Message.GetProtocolMessage().GetEditedMessage()
	}
	return evt, nil
}

// RequestHistorySync asks the user's primary phone for up to `count` older
// messages (capped at 50) preceding the oldest stored message in `chatJID`. The
// phone delivers the older messages asynchronously via an ON_DEMAND HistorySync
// event, which the background worker ingests into the store.
func (m *Manager) RequestHistorySync(ctx context.Context, account, chatJID string, count int) (int, error) {
	if m.historySyncOff {
		return 0, fmt.Errorf("history sync is disabled (WA_HISTORY_SYNC=off)")
	}
	if count <= 0 || count > maxOnDemandHistoryCount {
		count = maxOnDemandHistoryCount
	}
	cli, err := m.clientFor(ctx, account)
	if err != nil {
		return 0, err
	}
	variants := m.chatJIDVariants(ctx, account, chatJID)
	acctKey := m.acct(account)
	oldest, err := m.store.OldestMessage(ctx, acctKey, variants)
	if err != nil {
		return 0, err
	}
	if oldest == nil {
		return 0, fmt.Errorf("no stored messages found in chat %q to anchor on-demand history sync", chatJID)
	}
	anchorChat, err := types.ParseJID(oldest.ChatJID)
	if err != nil {
		return 0, fmt.Errorf("invalid anchor chat JID %q: %w", oldest.ChatJID, err)
	}
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     anchorChat,
			IsFromMe: oldest.FromMe,
		},
		ID:        oldest.ID,
		Timestamp: time.UnixMilli(oldest.TS),
	}
	reqMsg := cli.BuildHistorySyncRequest(info, count)
	resp, err := cli.SendPeerMessage(ctx, reqMsg)
	if err != nil {
		return 0, fmt.Errorf("send history sync request: %w", err)
	}
	m.log.Infof("history-sync on-demand requested account=%s count=%d oldest_ts=%d req_id=%s",
		maskPhone(acctKey), count, oldest.TS, resp.ID)
	return count, nil
}

func maskPhone(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "default"
	}
	if i := strings.IndexAny(s, ":@"); i > 0 {
		s = s[:i]
	}
	if len(s) <= 6 {
		return "***"
	}
	return s[:4] + "…" + s[len(s)-4:]
}
