package wa

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

// Inbound webhook: for the accounts listed in INBOUND_WEBHOOK_ACCOUNTS (e.g. a
// service's global number), every received direct message is POSTed to
// INBOUND_WEBHOOK_URL, signed with HMAC-SHA256 over "<ts>.<body>". Deliveries go
// through a persistent outbox so receiver outages and restarts lose nothing.

// WebhookConfig enables the inbound push. Empty URL = disabled.
type WebhookConfig struct {
	URL      string
	Secret   string
	Accounts map[string]bool // account keys (phone digits) allowed to push
}

// InboundEvent is the JSON body sent to the receiver.
type InboundEvent struct {
	Account   string `json:"account"`              // receiving account (phone digits)
	ID        string `json:"id"`                   // message id
	From      string `json:"from"`                 // sender phone (digits, E.164 without +); "" if unresolved
	FromJID   string `json:"from_jid"`             // raw sender JID (may be @lid)
	PushName  string `json:"push_name,omitempty"`  // sender's profile name
	TS        int64  `json:"ts"`                   // unix millis
	Type      string `json:"type"`                 // text | image | audio | video | document | sticker | button_reply | list_reply
	Text      string `json:"text,omitempty"`       // text or caption (for a reply, the label tapped)
	ReplyID   string `json:"reply_id,omitempty"`   // button_reply/list_reply: id of the button/row tapped
	MediaType string `json:"media_type,omitempty"` // as stored (download via GET /api/media/{id})
	QuotedID  string `json:"quoted_id,omitempty"`
}

const (
	outboxBatch   = 50
	outboxPoll    = 2 * time.Second
	outboxMaxAge  = 72 * time.Hour
	outboxTimeout = 15 * time.Second
)

// SetWebhook enables inbound push and starts the delivery loop (call before
// LoadAndConnect so no message slips through unpushed).
func (m *Manager) SetWebhook(ctx context.Context, cfg WebhookConfig) error {
	if cfg.URL == "" || cfg.Secret == "" || len(cfg.Accounts) == 0 {
		return nil
	}
	if err := m.store.InitOutbox(ctx); err != nil {
		return err
	}
	m.webhook = &cfg
	m.startOutbox(ctx)
	return nil
}

// Sign returns the X-Signature header value for ts and body.
func Sign(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// inboundFor decides whether a received message should be pushed and builds the
// event. Only 1:1 messages from others to a webhook-enabled account qualify.
func (m *Manager) inboundFor(ctx context.Context, account string, v *events.Message, msg appstore.Message) *InboundEvent {
	if m.webhook == nil || !m.webhook.Accounts[account] {
		return nil
	}
	info := v.Info
	if info.IsFromMe || info.IsGroup || info.Chat.IsBroadcastList() || info.Chat.Server == types.NewsletterServer ||
		info.Chat == types.StatusBroadcastJID {
		return nil
	}
	if msg.Body == "" && msg.MediaType == "" {
		return nil // reactions, receipts, protocol messages
	}
	from := ""
	switch {
	case info.Sender.Server == types.DefaultUserServer:
		from = info.Sender.User
	case info.SenderAlt.Server == types.DefaultUserServer:
		from = info.SenderAlt.User
	case info.Sender.Server == types.HiddenUserServer:
		if cli, err := m.clientFor(account); err == nil {
			if pn, err := cli.Store.LIDs.GetPNForLID(ctx, info.Sender.ToNonAD()); err == nil && !pn.IsEmpty() {
				from = pn.User
			}
		}
	}
	typ := "text"
	if msg.MediaType != "" {
		typ = msg.MediaType
	}
	text, replyID := msg.Body, ""
	if r := interactiveReply(v.Message); r != nil {
		typ, text, replyID = r.Kind+"_reply", r.Text, r.ID
	}
	ev := &InboundEvent{Account: account, ID: info.ID, From: from, FromJID: info.Sender.ToNonAD().String(),
		PushName: info.PushName, TS: info.Timestamp.UnixMilli(), Type: typ, Text: text, ReplyID: replyID, MediaType: msg.MediaType}
	if ci := v.Message.GetExtendedTextMessage().GetContextInfo(); ci != nil {
		ev.QuotedID = ci.GetStanzaID()
	}
	return ev
}

// pushInbound stores the event in the outbox (after the message itself is stored,
// so the receiver can download its media right away).
func (m *Manager) pushInbound(ev *InboundEvent) {
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), ingestWriteTimeout)
	defer cancel()
	if err := m.store.EnqueueOutbox(ctx, ev.Account, ev.ID, body); err != nil {
		m.warnThrottled(&m.ingestFailLog, "webhook outbox enqueue failing: %v", err)
		return
	}
	select {
	case m.outboxKick <- struct{}{}:
	default:
	}
}

// startOutbox runs the delivery loop.
func (m *Manager) startOutbox(ctx context.Context) {
	if m.webhook == nil {
		return
	}
	client := &http.Client{Timeout: outboxTimeout}
	go func() {
		t := time.NewTicker(outboxPoll)
		defer t.Stop()
		purge := time.NewTicker(time.Hour)
		defer purge.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-purge.C:
				if n, err := m.store.PurgeOutbox(ctx, outboxMaxAge); err == nil && n > 0 {
					m.log.Warnf("webhook outbox: dropped %d delivery(ies) older than %s", n, outboxMaxAge)
				}
			case <-t.C:
			case <-m.outboxKick:
			}
			items, err := m.store.DueOutbox(ctx, outboxBatch)
			if err != nil {
				continue
			}
			for _, it := range items {
				if err := m.deliver(ctx, client, it.Payload); err != nil {
					back := time.Duration(1<<min(it.Attempts, 9)) * time.Second // 1s … ~8.5min
					_ = m.store.RetryOutbox(ctx, it.ID, it.Attempts+1, time.Now().Add(back))
					m.warnThrottled(&m.ingestDropLog, "webhook delivery failing (attempt %d): %v", it.Attempts+1, err)
					break // keep order per receiver; retry the head first
				}
				_ = m.store.AckOutbox(ctx, it.ID)
			}
		}
	}()
}

func (m *Manager) deliver(ctx context.Context, client *http.Client, body []byte) error {
	ts := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.webhook.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Signature", Sign(m.webhook.Secret, ts, body))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("receiver answered %d", resp.StatusCode)
	}
	return nil
}

// ParseAccounts turns "5521999990000, 551188887777" into a set of account keys.
func ParseAccounts(s string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		d := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, p)
		if d != "" {
			out[d] = true
		}
	}
	return out
}
