// Helpers that back the MCP App's cards: cheap lookups the tool layer needs to
// build a rendered view (chat previews, sender names, the account's own JID)
// without a round-trip per row.
package wa

import (
	"context"
	"strings"

	"go.mau.fi/whatsmeow/types"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

// SelfJID returns the account's own JID (zero value when not connected).
func (m *Manager) SelfJID(account string) types.JID {
	cli, err := m.clientFor(account)
	if err != nil || cli.Store.ID == nil {
		return types.JID{}
	}
	return *cli.Store.ID
}

// ChatPreviews returns the most recent message per chat in one query, so a chat
// list can show a preview line without N round-trips.
func (m *Manager) ChatPreviews(ctx context.Context, account string, jids []string) map[string]appstore.Message {
	if len(jids) == 0 {
		return map[string]appstore.Message{}
	}
	out, err := m.store.LatestPerChat(ctx, m.acct(account), jids)
	if err != nil {
		return map[string]appstore.Message{}
	}
	return out
}

// SenderNames resolves display names for the distinct senders of a message set
// (group threads show who wrote each line). Contacts are read one by one, but
// only for the handful of distinct senders in the page.
func (m *Manager) SenderNames(ctx context.Context, account string, msgs []appstore.Message) map[string]string {
	cli, err := m.clientFor(account)
	if err != nil {
		return map[string]string{}
	}
	seen := make(map[string]string, 8)
	for _, msg := range msgs {
		if msg.FromMe || msg.SenderJID == "" {
			continue
		}
		if _, ok := seen[msg.SenderJID]; ok {
			continue
		}
		jid, err := types.ParseJID(msg.SenderJID)
		if err != nil {
			continue
		}
		if info, err := cli.Store.Contacts.GetContact(ctx, jid); err == nil {
			if n := bestContactName(info); n != "" {
				seen[msg.SenderJID] = n
				continue
			}
		}
		seen[msg.SenderJID] = "+" + jid.User
	}
	return seen
}

// ChatName resolves one chat's display name, preferring what is already
// stored. Asking WhatsApp costs a fetch of every joined group, which is far too
// much work to name a single conversation — so that path is only taken when the
// database has nothing, and its result is remembered for next time.
func (m *Manager) ChatName(ctx context.Context, account, jid string) string {
	if name, err := m.store.ChatName(ctx, m.acct(account), jid); err == nil && name != "" {
		return name
	}
	chats := []appstore.Chat{{JID: jid}}
	m.fillChatNames(ctx, account, chats)
	return chats[0].Name
}

// SearchContacts returns contacts matching a case-insensitive substring of the
// name or number, capped at limit. An empty query returns the first `limit`
// contacts by name. The second return is the total number of matches.
func (m *Manager) SearchContacts(ctx context.Context, account, query string, limit int) ([]Contact, int, error) {
	all, err := m.Contacts(ctx, account)
	if err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := strings.ToLower(strings.TrimSpace(query))
	matched := make([]Contact, 0, limit)
	total := 0
	for _, c := range all {
		if q != "" && !contactMatches(c, q) {
			continue
		}
		total++
		if len(matched) < limit {
			matched = append(matched, c)
		}
	}
	sortContacts(matched)
	return matched, total, nil
}

func contactMatches(c Contact, q string) bool {
	for _, f := range []string{c.FullName, c.PushName, c.BusinessName, c.FirstName, c.Number, c.JID} {
		if f != "" && strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// sortContacts puts named contacts first (alphabetically), then the rest — a
// list of bare numbers at the top is useless in a picker.
func sortContacts(cs []Contact) {
	name := func(c Contact) string {
		return strings.ToLower(DisplayName(c))
	}
	named := func(c Contact) bool {
		return c.FullName != "" || c.PushName != "" || c.BusinessName != ""
	}
	// insertion sort: the slice is bounded by `limit` (<=500)
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0; j-- {
			a, b := cs[j-1], cs[j]
			if named(a) != named(b) {
				if named(b) {
					cs[j-1], cs[j] = b, a
					continue
				}
				break
			}
			if name(a) > name(b) {
				cs[j-1], cs[j] = b, a
				continue
			}
			break
		}
	}
}

// DisplayName is the best human label for a contact.
func DisplayName(c Contact) string {
	switch {
	case c.FullName != "":
		return c.FullName
	case c.PushName != "":
		return c.PushName
	case c.BusinessName != "":
		return c.BusinessName
	case c.Number != "":
		return "+" + c.Number
	default:
		return c.RedactedPhone
	}
}

// PushName is the account's own display name (empty when not connected).
func (m *Manager) PushName(account string) string {
	cli, err := m.clientFor(account)
	if err != nil {
		return ""
	}
	return cli.Store.PushName
}

// ProfileCardView is the contact-detail payload the MCP App renders. It lives
// here (not in mcpserver) because it needs the whatsmeow client to enrich the
// contact with its status and picture.
type ProfileCardView struct {
	Kind       string  `json:"kind"`
	Contact    Contact `json:"contact"`
	Status     string  `json:"status,omitempty"`
	PictureURL string  `json:"picture_url,omitempty"`
	IsBusiness bool    `json:"is_business,omitempty"`
}

// ProfileCard merges the stored contact with the live UserInfo (status, picture)
// for one number, so the UI can show a complete contact card from one tool call.
func (m *Manager) ProfileCard(ctx context.Context, account, number string, info map[types.JID]types.UserInfo) ProfileCardView {
	out := ProfileCardView{Kind: "contact"}
	cli, err := m.clientFor(account)
	if err != nil {
		return out
	}
	var jid types.JID
	var ui types.UserInfo
	for j, v := range info {
		jid, ui = j, v
		break
	}
	if jid.IsEmpty() {
		if parsed, err := resolveJID(number); err == nil {
			jid = parsed
		}
	}
	out.Status = ui.Status
	if ui.VerifiedName != nil && ui.VerifiedName.Details != nil {
		out.IsBusiness = true
	}
	if jid.IsEmpty() {
		return out
	}
	out.Contact = Contact{JID: jid.String(), Number: jid.ToNonAD().User}
	if c, err := cli.Store.Contacts.GetContact(ctx, jid.ToNonAD()); err == nil {
		out.Contact.Found = c.Found
		out.Contact.FirstName = c.FirstName
		out.Contact.FullName = c.FullName
		out.Contact.PushName = c.PushName
		out.Contact.BusinessName = c.BusinessName
		out.Contact.RedactedPhone = c.RedactedPhone
		if c.BusinessName != "" {
			out.IsBusiness = true
		}
	}
	if url, err := m.ProfilePictureURL(ctx, account, jid.String()); err == nil {
		out.PictureURL = url
	}
	return out
}
