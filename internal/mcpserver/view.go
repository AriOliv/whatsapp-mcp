// View models for the MCP App (interactive UI).
//
// Every tool that has a UI returns one of these as structured content; the app
// bundle switches on the `Kind` field to pick a card. This file is the source of
// truth for that contract — keep app/src/lib/types.ts in sync.
//
// The shapes are deliberately small: a tool result larger than ~150k characters
// is spilled to the host's sandbox filesystem, and the app then receives a file
// pointer instead of the data, so it never hydrates. Lists are capped and
// detail is fetched on demand from the app.
package mcpserver

import (
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

// Kind discriminators. Must match the Kind union in app/src/lib/types.ts.
const (
	kindChats        = "chats"
	kindMessages     = "messages"
	kindContacts     = "contacts"
	kindContact      = "contact"
	kindGroups       = "groups"
	kindGroup        = "group"
	kindParticipants = "participants"
	kindNumbers      = "numbers"
	kindMedia        = "media"
	kindSent         = "sent"
	kindReceipt      = "receipt"
	kindAccount      = "account"
	kindPrivacy      = "privacy"
	kindBusiness     = "business"
	kindPoll         = "poll"
	kindLocation     = "location"
	kindInvite       = "invite"
	kindDevices      = "devices"
)

/* ------------------------------------------------------------------ chats */

type chatView struct {
	JID          string `json:"jid"`
	Name         string `json:"name"`
	LastTSMillis int64  `json:"last_ts_millis"`
	Kind         string `json:"kind"`
	Preview      string `json:"preview,omitempty"`
	MediaType    string `json:"media_type,omitempty"`
	FromMe       bool   `json:"from_me,omitempty"`
}

type chatsView struct {
	Kind    string          `json:"kind"`
	Chats   []chatView      `json:"chats"`
	Total   int             `json:"total"`
	Account *accountSummary `json:"account,omitempty"`
}

// chatKindOf classifies a chat JID so the UI can pick the right icon and label.
func chatKindOf(jid string) string {
	switch {
	case strings.HasSuffix(jid, "@g.us"):
		return "group"
	case strings.HasSuffix(jid, "@newsletter"):
		return "newsletter"
	case jid == "status@broadcast":
		return "status"
	default:
		return "dm"
	}
}

func newChatsView(chats []appstore.Chat, previews map[string]appstore.Message, acct *accountSummary) chatsView {
	out := make([]chatView, 0, len(chats))
	for _, c := range chats {
		v := chatView{JID: c.JID, Name: c.Name, LastTSMillis: c.LastTS, Kind: chatKindOf(c.JID)}
		if m, ok := previews[c.JID]; ok {
			v.Preview = truncate(m.Body, 120)
			v.MediaType = m.MediaType
			v.FromMe = m.FromMe
		}
		out = append(out, v)
	}
	return chatsView{Kind: kindChats, Chats: out, Total: len(out), Account: acct}
}

/* --------------------------------------------------------------- messages */

type messageView struct {
	ID         string `json:"id"`
	ChatJID    string `json:"chat_jid"`
	SenderJID  string `json:"sender_jid,omitempty"`
	SenderName string `json:"sender_name,omitempty"`
	FromMe     bool   `json:"from_me"`
	TSMillis   int64  `json:"ts_millis"`
	Body       string `json:"body"`
	MediaType  string `json:"media_type,omitempty"`
	HasMedia   bool   `json:"has_media,omitempty"`
}

type messagesView struct {
	Kind    string        `json:"kind"`
	Chat    chatRef       `json:"chat"`
	Msgs    []messageView `json:"messages"`
	HasMore bool          `json:"has_more,omitempty"`
}

type chatRef struct {
	JID              string `json:"jid"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	ParticipantCount int    `json:"participant_count,omitempty"`
}

func newMessagesView(chat chatRef, msgs []appstore.Message, names map[string]string, hasMore bool) messagesView {
	out := make([]messageView, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageView{
			ID:         m.ID,
			ChatJID:    m.ChatJID,
			SenderJID:  m.SenderJID,
			SenderName: names[m.SenderJID],
			FromMe:     m.FromMe,
			TSMillis:   m.TS,
			Body:       m.Body,
			MediaType:  m.MediaType,
			HasMedia:   m.MediaType != "",
		})
	}
	if chat.Kind == "" {
		chat.Kind = chatKindOf(chat.JID)
	}
	return messagesView{Kind: kindMessages, Chat: chat, Msgs: out, HasMore: hasMore}
}

/* --------------------------------------------------------------- contacts */

type contactsView struct {
	Kind     string       `json:"kind"`
	Contacts []wa.Contact `json:"contacts"`
	Total    int          `json:"total"`
	Shown    int          `json:"shown"`
	Query    string       `json:"query,omitempty"`
}

type contactView struct {
	Kind       string     `json:"kind"`
	Contact    wa.Contact `json:"contact"`
	Status     string     `json:"status,omitempty"`
	PictureURL string     `json:"picture_url,omitempty"`
	IsBusiness bool       `json:"is_business,omitempty"`
}

/* ----------------------------------------------------------------- groups */

type groupView struct {
	JID              string `json:"jid"`
	Name             string `json:"name"`
	Topic            string `json:"topic,omitempty"`
	ParticipantCount int    `json:"participant_count"`
	IsAnnounce       bool   `json:"is_announce,omitempty"`
	IsLocked         bool   `json:"is_locked,omitempty"`
	IsEphemeral      bool   `json:"is_ephemeral,omitempty"`
	IsCommunity      bool   `json:"is_community,omitempty"`
	Created          string `json:"created,omitempty"`
	AmAdmin          bool   `json:"am_admin,omitempty"`
}

type groupsView struct {
	Kind   string      `json:"kind"`
	Groups []groupView `json:"groups"`
	Total  int         `json:"total"`
	Shown  int         `json:"shown"`
	Query  string      `json:"query,omitempty"`
}

type participantView struct {
	JID          string `json:"jid"`
	Name         string `json:"name,omitempty"`
	Number       string `json:"number,omitempty"`
	IsAdmin      bool   `json:"is_admin,omitempty"`
	IsSuperAdmin bool   `json:"is_super_admin,omitempty"`
}

type groupDetailView struct {
	Kind         string            `json:"kind"`
	Group        groupView         `json:"group"`
	Participants []participantView `json:"participants,omitempty"`
	InviteLink   string            `json:"invite_link,omitempty"`
}

type participantsView struct {
	Kind         string            `json:"kind"`
	Group        groupRef          `json:"group"`
	Participants []participantView `json:"participants"`
}

type groupRef struct {
	JID  string `json:"jid"`
	Name string `json:"name,omitempty"`
}

// newGroupView flattens whatsmeow's embedded group structs. selfJID (may be
// empty) marks whether the caller is an admin, which gates the UI's affordances.
func newGroupView(g *types.GroupInfo, selfJID types.JID) groupView {
	v := groupView{
		JID:              g.JID.String(),
		Name:             g.Name,
		Topic:            g.Topic,
		ParticipantCount: g.ParticipantCount,
		IsAnnounce:       g.IsAnnounce,
		IsLocked:         g.IsLocked,
		IsEphemeral:      g.IsEphemeral,
		IsCommunity:      g.IsParent,
	}
	if v.ParticipantCount == 0 {
		v.ParticipantCount = len(g.Participants)
	}
	if !g.GroupCreated.IsZero() {
		v.Created = g.GroupCreated.Format("2006-01-02")
	}
	if !selfJID.IsEmpty() {
		for _, p := range g.Participants {
			if sameUser(p, selfJID) && (p.IsAdmin || p.IsSuperAdmin) {
				v.AmAdmin = true
				break
			}
		}
	}
	return v
}

// sameUser reports whether a participant is the given account, comparing both
// the phone JID and the privacy (@lid) JID since either may be the addressing
// mode for a group.
func sameUser(p types.GroupParticipant, self types.JID) bool {
	su := self.ToNonAD().User
	return p.JID.User == su || p.PhoneNumber.User == su || p.LID.User == su
}

func newParticipantViews(ps []types.GroupParticipant) []participantView {
	out := make([]participantView, 0, len(ps))
	for _, p := range ps {
		v := participantView{
			JID:          p.JID.String(),
			Name:         p.DisplayName,
			IsAdmin:      p.IsAdmin,
			IsSuperAdmin: p.IsSuperAdmin,
		}
		switch {
		case p.PhoneNumber.User != "":
			v.Number = p.PhoneNumber.User
		case p.JID.Server == types.DefaultUserServer:
			v.Number = p.JID.User
		}
		out = append(out, v)
	}
	return out
}

/* ---------------------------------------------------------------- numbers */

type numberView struct {
	Query        string `json:"query"`
	JID          string `json:"jid,omitempty"`
	Registered   bool   `json:"registered"`
	BusinessName string `json:"business_name,omitempty"`
}

type numbersView struct {
	Kind    string       `json:"kind"`
	Numbers []numberView `json:"numbers"`
}

func newNumbersView(res []types.IsOnWhatsAppResponse) numbersView {
	out := make([]numberView, 0, len(res))
	for _, r := range res {
		v := numberView{Query: r.Query, Registered: r.IsIn}
		if r.IsIn {
			v.JID = r.JID.String()
		}
		if r.VerifiedName != nil && r.VerifiedName.Details != nil {
			v.BusinessName = r.VerifiedName.Details.GetVerifiedName()
		}
		out = append(out, v)
	}
	return numbersView{Kind: kindNumbers, Numbers: out}
}

/* ----------------------------------------------------- acks & small cards */

type mediaView struct {
	Kind       string `json:"kind"`
	Mimetype   string `json:"mimetype"`
	Filename   string `json:"filename"`
	Bytes      int    `json:"bytes"`
	DataBase64 string `json:"data_base64"`
	MessageID  string `json:"message_id,omitempty"`
}

type sentView struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	To       string `json:"to"`
	ToName   string `json:"to_name,omitempty"`
	What     string `json:"what"`
	Preview  string `json:"preview,omitempty"`
	TSMillis int64  `json:"ts_millis,omitempty"`
}

func newSentView(id, to, what, preview string) sentView {
	return sentView{
		Kind:     kindSent,
		ID:       id,
		To:       to,
		What:     what,
		Preview:  truncate(preview, 160),
		TSMillis: time.Now().UnixMilli(),
	}
}

type receiptView struct {
	Kind   string `json:"kind"`
	OK     bool   `json:"ok"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
	Target string `json:"target,omitempty"`
}

func newReceiptView(action, detail, target string, err error) receiptView {
	return receiptView{Kind: kindReceipt, OK: err == nil, Action: action, Detail: detail, Target: target}
}

type accountSummary struct {
	JID       string `json:"jid"`
	Number    string `json:"number,omitempty"`
	Connected bool   `json:"connected"`
	LoggedIn  bool   `json:"logged_in"`
	PushName  string `json:"push_name,omitempty"`
}

type accountView struct {
	Kind    string         `json:"kind"`
	Account accountSummary `json:"account"`
}

type privacyView struct {
	Kind     string            `json:"kind"`
	Settings map[string]string `json:"settings"`
}

func newPrivacyView(p types.PrivacySettings) privacyView {
	return privacyView{Kind: kindPrivacy, Settings: map[string]string{
		"group_add":     string(p.GroupAdd),
		"last_seen":     string(p.LastSeen),
		"status":        string(p.Status),
		"profile":       string(p.Profile),
		"read_receipts": string(p.ReadReceipts),
		"call_add":      string(p.CallAdd),
		"online":        string(p.Online),
		"messages":      string(p.Messages),
	}}
}

type businessHour struct {
	Day   string `json:"day"`
	Mode  string `json:"mode"`
	Open  string `json:"open,omitempty"`
	Close string `json:"close,omitempty"`
}

type businessView struct {
	Kind       string         `json:"kind"`
	JID        string         `json:"jid"`
	Name       string         `json:"name,omitempty"`
	Address    string         `json:"address,omitempty"`
	Email      string         `json:"email,omitempty"`
	Categories []string       `json:"categories,omitempty"`
	Hours      []businessHour `json:"hours,omitempty"`
	Timezone   string         `json:"timezone,omitempty"`
}

func newBusinessView(p *types.BusinessProfile) businessView {
	v := businessView{
		Kind:     kindBusiness,
		JID:      p.JID.String(),
		Address:  p.Address,
		Email:    p.Email,
		Timezone: p.BusinessHoursTimeZone,
	}
	for _, c := range p.Categories {
		v.Categories = append(v.Categories, c.Name)
	}
	for _, h := range p.BusinessHours {
		v.Hours = append(v.Hours, businessHour{Day: h.DayOfWeek, Mode: h.Mode, Open: h.OpenTime, Close: h.CloseTime})
	}
	return v
}

type pollView struct {
	Kind       string   `json:"kind"`
	ID         string   `json:"id"`
	To         string   `json:"to"`
	Name       string   `json:"name"`
	Options    []string `json:"options"`
	Selectable int      `json:"selectable"`
}

type locationView struct {
	Kind      string  `json:"kind"`
	ID        string  `json:"id"`
	To        string  `json:"to"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Name      string  `json:"name,omitempty"`
	Address   string  `json:"address,omitempty"`
}

type inviteView struct {
	Kind       string `json:"kind"`
	GroupJID   string `json:"group_jid"`
	GroupName  string `json:"group_name,omitempty"`
	InviteLink string `json:"invite_link"`
}

type devicesView struct {
	Kind    string   `json:"kind"`
	Devices []string `json:"devices"`
}

/* ------------------------------------------------------------------ util */

// truncate clips a preview to n runes, appending an ellipsis when it cut.
func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}
