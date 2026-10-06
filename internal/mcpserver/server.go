// Package mcpserver builds the MCP server and registers the WhatsApp tools.
// Tools are named whatsapp_* and map directly to whatsmeow calls — there is no
// Evolution API in the loop.
//
// Tools that have a card return a typed view (see view.go) as structured
// content and carry `_meta.ui.resourceUri` (see app.go), so Claude renders the
// MCP App instead of raw JSON. The JSON text mirror is still returned for the
// model and for hosts without app support.
package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AriOliv/whatsapp-mcp/internal/oauth"
	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

// Build wires all tools against the whatsmeow manager and returns the server.
func Build(mgr *wa.Manager) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "whatsapp-mcp", Version: "2.0.0-whatsmeow"}, nil)

	// Without this, a failing tool is invisible server-side: the error travels
	// back inside the result and never reaches the log.
	s.AddReceivingMiddleware(logToolFailures)

	// The interactive UI every card renders from.
	registerApp(s)

	// --- messaging ---
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_send_text", Description: "Send a WhatsApp text message."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendTextArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendText(ctx, acct(ctx), in.Number, in.Text)
			if err != nil {
				return done(nil, err)
			}
			v := newSentView(id, in.Number, "text", in.Text)
			v.ToName = mgr.ChatName(ctx, acct(ctx), in.Number)
			return done(v, nil)
		})

	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_send_media", Description: "Send an image/video/document (URL or base64)."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendMediaArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendMedia(ctx, acct(ctx), in.Number, in.Mediatype, in.Media, in.Caption, in.Mimetype, in.FileName)
			if err != nil {
				return done(nil, err)
			}
			v := newSentView(id, in.Number, "media", firstNonEmpty(in.Caption, in.FileName))
			v.ToName = mgr.ChatName(ctx, acct(ctx), in.Number)
			return done(v, nil)
		})

	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_send_audio", Description: "Send a voice/audio message (URL or base64)."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendAudioArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendMedia(ctx, acct(ctx), in.Number, "audio", in.Audio, "", "", "")
			if err != nil {
				return done(nil, err)
			}
			v := newSentView(id, in.Number, "audio", "")
			v.ToName = mgr.ChatName(ctx, acct(ctx), in.Number)
			return done(v, nil)
		})

	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_send_reaction", Description: "React to a message with an emoji (empty removes)."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in reactionArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.React(ctx, acct(ctx), in.Key.RemoteJID, in.Key.RemoteJID, in.Key.ID, in.Reaction)
			if err != nil {
				return done(nil, err)
			}
			v := newSentView(id, in.Key.RemoteJID, "reaction", in.Reaction)
			v.ToName = mgr.ChatName(ctx, acct(ctx), in.Key.RemoteJID)
			return done(v, nil)
		})

	// --- contacts / numbers ---
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_check_numbers", Description: "Check which numbers are registered on WhatsApp."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in checkNumbersArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.CheckNumbers(ctx, acct(ctx), in.Numbers)
			if err != nil {
				return done(nil, err)
			}
			return done(newNumbersView(res), nil)
		})

	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_find_contacts", Description: "Search the account's stored contacts by name or number. Returns a capped page (default 50, max 500) — an account can hold tens of thousands of contacts, so always pass a query when looking for someone."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in findContactsArgs) (*mcp.CallToolResult, any, error) {
			limit := clamp(in.Limit, 50, 500)
			list, total, err := mgr.SearchContacts(ctx, acct(ctx), in.Query, limit)
			if err != nil {
				return done(nil, err)
			}
			return done(contactsView{Kind: kindContacts, Contacts: list, Total: total, Shown: len(list), Query: in.Query}, nil)
		})

	// --- chats & messages (our own store) ---
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_find_chats", Description: "List recent conversations (from local history), newest first, each with the last message preview."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in listChatsArgs) (*mcp.CallToolResult, any, error) {
			chats, err := mgr.ListChats(ctx, acct(ctx), in.Limit)
			if err != nil {
				return done(nil, err)
			}
			jids := make([]string, 0, len(chats))
			for _, c := range chats {
				jids = append(jids, c.JID)
			}
			return done(newChatsView(chats, mgr.ChatPreviews(ctx, acct(ctx), jids), accountOf(ctx, mgr)), nil)
		})

	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_find_messages", Description: "List messages in a chat (from local history), newest first."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in listMessagesArgs) (*mcp.CallToolResult, any, error) {
			a := acct(ctx)
			msgs, err := mgr.ListMessages(ctx, a, in.RemoteJID, in.Limit)
			if err != nil {
				return done(nil, err)
			}
			ref := chatRef{JID: in.RemoteJID, Name: mgr.ChatName(ctx, a, in.RemoteJID)}
			return done(newMessagesView(ref, msgs, mgr.SenderNames(ctx, a, msgs), len(msgs) == clamp(in.Limit, 50, 500)), nil)
		})

	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_download_media", Description: "Download the media of a stored message (image, audio/voice note, video, document, or sticker) by its message id (from find_messages). Returns base64 data plus mimetype and a suggested filename. Only messages received or sent after media support was enabled carry downloadable media."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in downloadMediaArgs) (*mcp.CallToolResult, any, error) {
			data, mime, name, err := mgr.DownloadMedia(ctx, acct(ctx), in.ID)
			if err != nil {
				return done(nil, err)
			}
			return done(mediaView{
				Kind:       kindMedia,
				Mimetype:   mime,
				Filename:   name,
				Bytes:      len(data),
				DataBase64: base64.StdEncoding.EncodeToString(data),
				MessageID:  in.ID,
			}, nil)
		})

	// --- groups ---
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_group_fetch_all", Description: "List joined groups (name, topic, size, flags), optionally filtered by a name query. Returns a capped page; use whatsapp_group_info or whatsapp_group_participants for one group's members."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in findGroupsArgs) (*mcp.CallToolResult, any, error) {
			a := acct(ctx)
			res, err := mgr.Groups(ctx, a)
			if err != nil {
				return done(nil, err)
			}
			limit := clamp(in.Limit, 50, 500)
			self := mgr.SelfJID(a)
			q := strings.ToLower(strings.TrimSpace(in.Query))
			out, total := make([]groupView, 0, limit), 0
			for _, g := range res {
				if q != "" && !strings.Contains(strings.ToLower(g.Name), q) && !strings.Contains(strings.ToLower(g.Topic), q) {
					continue
				}
				total++
				if len(out) < limit {
					out = append(out, newGroupView(g, self))
				}
			}
			return done(groupsView{Kind: kindGroups, Groups: out, Total: total, Shown: len(out), Query: in.Query}, nil)
		})

	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_group_info", Description: "Get info for one group by JID, including its participants."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupJidArgs) (*mcp.CallToolResult, any, error) {
			a := acct(ctx)
			g, err := mgr.GroupInfo(ctx, a, in.GroupJID)
			if err != nil {
				return done(nil, err)
			}
			return done(groupDetailView{
				Kind:         kindGroup,
				Group:        newGroupView(g, mgr.SelfJID(a)),
				Participants: newParticipantViews(g.Participants),
			}, nil)
		})

	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_group_create", Description: "Create a group with participants."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupCreateArgs) (*mcp.CallToolResult, any, error) {
			a := acct(ctx)
			g, err := mgr.CreateGroup(ctx, a, in.Subject, in.Participants)
			if err != nil {
				return done(nil, err)
			}
			return done(groupDetailView{
				Kind:         kindGroup,
				Group:        newGroupView(g, mgr.SelfJID(a)),
				Participants: newParticipantViews(g.Participants),
			}, nil)
		})

	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_group_invite_code", Description: "Get a group's invite link."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupJidArgs) (*mcp.CallToolResult, any, error) {
			a := acct(ctx)
			link, err := mgr.InviteLink(ctx, a, in.GroupJID, false)
			if err != nil {
				return done(nil, err)
			}
			return done(inviteView{Kind: kindInvite, GroupJID: in.GroupJID, GroupName: mgr.ChatName(ctx, a, in.GroupJID), InviteLink: link}, nil)
		})

	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_group_update_participant", Description: "Add/remove/promote/demote group members."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupParticipantsArgs) (*mcp.CallToolResult, any, error) {
			a := acct(ctx)
			err := mgr.UpdateParticipants(ctx, a, in.GroupJID, in.Action, in.Participants)
			if err != nil {
				return done(nil, err)
			}
			return done(newReceiptView(participantActionLabel(in.Action), plural(len(in.Participants), "participante", "participantes"), mgr.ChatName(ctx, a, in.GroupJID), nil), nil)
		})

	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_group_update_subject", Description: "Change a group's subject/name."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupSubjectArgs) (*mcp.CallToolResult, any, error) {
			err := mgr.SetGroupName(ctx, acct(ctx), in.GroupJID, in.Subject)
			if err != nil {
				return done(nil, err)
			}
			return done(newReceiptView("Nome do grupo alterado", in.Subject, in.GroupJID, nil), nil)
		})

	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_group_leave", Description: "Leave a group."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupJidArgs) (*mcp.CallToolResult, any, error) {
			a := acct(ctx)
			name := mgr.ChatName(ctx, a, in.GroupJID)
			err := mgr.LeaveGroup(ctx, a, in.GroupJID)
			if err != nil {
				return done(nil, err)
			}
			return done(newReceiptView("Você saiu do grupo", name, in.GroupJID, nil), nil)
		})

	// --- messaging extras ---
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_send_sticker", Description: "Send a sticker (webp, URL or base64)."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendStickerArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendSticker(ctx, acct(ctx), in.Number, in.Sticker)
			if err != nil {
				return done(nil, err)
			}
			v := newSentView(id, in.Number, "sticker", "")
			v.ToName = mgr.ChatName(ctx, acct(ctx), in.Number)
			return done(v, nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_send_location", Description: "Send a location pin."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendLocationArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendLocation(ctx, acct(ctx), in.Number, in.Latitude, in.Longitude, in.Name, in.Address)
			if err != nil {
				return done(nil, err)
			}
			return done(locationView{Kind: kindLocation, ID: id, To: in.Number, Latitude: in.Latitude, Longitude: in.Longitude, Name: in.Name, Address: in.Address}, nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_send_contact", Description: "Send a contact card."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendContactArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendContact(ctx, acct(ctx), in.Number, in.FullName, in.PhoneNumber)
			if err != nil {
				return done(nil, err)
			}
			v := newSentView(id, in.Number, "contact", in.FullName)
			v.ToName = mgr.ChatName(ctx, acct(ctx), in.Number)
			return done(v, nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_send_poll", Description: "Send a poll."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendPollArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendPoll(ctx, acct(ctx), in.Number, in.Name, in.Values, in.SelectableCount)
			if err != nil {
				return done(nil, err)
			}
			return done(pollView{Kind: kindPoll, ID: id, To: in.Number, Name: in.Name, Options: in.Values, Selectable: in.SelectableCount}, nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_send_buttons", Description: "Send a message with buttons. " +
		"Button types: quick_reply (up to 3; the tap comes back as a reply with its id), url (opens an https link), " +
		"call (dials a number), copy (copies a code, e.g. a PIX key). Up to 10 buttons. " +
		"Unofficial-client feature: if WhatsApp refuses it, set fallbackText=true to send the same content as plain text. " +
		"If buttons are accepted but don't render, retry with flavor=full."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendButtonsArgs) (*mcp.CallToolResult, any, error) {
			spec := wa.ButtonsSpec{Text: in.Text, Title: in.Title, Footer: in.Footer, Buttons: in.Buttons}
			res, err := mgr.SendButtons(ctx, acct(ctx), in.Number, spec, in.Flavor, in.FallbackText)
			if err != nil {
				return done(nil, err)
			}
			return done(newInteractiveSentView(ctx, mgr, res, in.Number, "buttons", in.Text), nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_send_form", Description: "Send a form to fill outside the chat. " +
		"The message carries a button that opens a web page with the fields; the answers never appear in the chat " +
		"(the person gets a short receipt). Fields: text, textarea, email, phone, number, date (YYYY-MM-DD), cpf, cnpj " +
		"(check digits validated), select/radio (with options), checkbox (with options = multiple choice, without = a " +
		"single yes/no). The link is single use and expires (default 24h). Read the answers with whatsapp_get_form."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendFormArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.SendForm(ctx, acct(ctx), in.Number, in.FormSpec)
			if err != nil {
				return done(nil, err)
			}
			return done(formSentView{FormSent: res, To: in.Number}, nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_get_form", Description: "Get a form sent with whatsapp_send_form: " +
		"status (open, submitted, expired) and, once submitted, the answers by field id."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in getFormArgs) (*mcp.CallToolResult, any, error) {
			v, err := mgr.GetForm(ctx, acct(ctx), in.FormID)
			if err != nil {
				return done(nil, err)
			}
			return done(v, nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_send_flow", Description: "EXPERIMENTAL. Send a WhatsApp Flow " +
		"(a native full-screen form) as a galaxy_message button. The recipient's app loads the screens from Meta by flowId, " +
		"so it normally only opens for flows published on a WhatsApp Business API account; from this (personal) account it " +
		"may not open at all. The filled form comes back as a reply with the flowToken and the fields. " +
		"For a form that always works, use whatsapp_send_form."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendFlowArgs) (*mcp.CallToolResult, any, error) {
			spec := wa.FlowSpec{Text: in.Text, Title: in.Title, Footer: in.Footer, CTA: in.CTA, FlowID: in.FlowID,
				FlowToken: in.FlowToken, Mode: in.Mode, Action: in.Action, Screen: in.Screen, Data: in.Data,
				FlowJSON: in.FlowJSON, Extra: in.Extra, DummyReply: in.DummyReply}
			res, err := mgr.SendFlow(ctx, acct(ctx), in.Number, spec, in.Flavor, in.FallbackText)
			if err != nil {
				return done(nil, err)
			}
			return done(newInteractiveSentView(ctx, mgr, res, in.Number, "flow", in.Text), nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_send_list", Description: "Send a list message: one button that opens " +
		"a menu of options grouped in sections (up to 10 sections, 10 rows each). The row picked comes back as a reply with its id. " +
		"Unofficial-client feature: set fallbackText=true to send the options as a numbered plain-text list if WhatsApp refuses it."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendListArgs) (*mcp.CallToolResult, any, error) {
			spec := wa.ListSpec{Text: in.Text, Title: in.Title, Footer: in.Footer, ButtonText: in.ButtonText, Sections: in.Sections}
			res, err := mgr.SendList(ctx, acct(ctx), in.Number, spec, in.FallbackText)
			if err != nil {
				return done(nil, err)
			}
			return done(newInteractiveSentView(ctx, mgr, res, in.Number, "list", in.Text), nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_delete_message", Description: "Delete a message for everyone (revoke)."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in deleteMessageArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.DeleteMessage(ctx, acct(ctx), in.RemoteJID, in.ID)
			if err != nil {
				return done(nil, err)
			}
			v := newSentView(id, in.RemoteJID, "delete", "")
			v.ToName = mgr.ChatName(ctx, acct(ctx), in.RemoteJID)
			return done(v, nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_update_message", Description: "Edit a sent text message."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in updateMessageArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.EditMessage(ctx, acct(ctx), in.RemoteJID, in.ID, in.Text)
			if err != nil {
				return done(nil, err)
			}
			v := newSentView(id, in.RemoteJID, "edit", in.Text)
			v.ToName = mgr.ChatName(ctx, acct(ctx), in.RemoteJID)
			return done(v, nil)
		})

	// --- presence / read ---
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_set_presence", Description: "Set global presence (available|unavailable)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in setPresenceArgs) (*mcp.CallToolResult, any, error) {
			err := mgr.SetPresence(ctx, acct(ctx), in.Presence)
			return done(map[string]bool{"ok": err == nil}, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_send_chat_presence", Description: "Send typing/recording indicator (composing|recording|paused)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in chatPresenceArgs) (*mcp.CallToolResult, any, error) {
			err := mgr.ChatPresence(ctx, acct(ctx), in.Number, in.Presence)
			return done(map[string]bool{"ok": err == nil}, err)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_mark_read", Description: "Mark messages as read in a chat."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in markReadArgs) (*mcp.CallToolResult, any, error) {
			a := acct(ctx)
			err := mgr.MarkRead(ctx, a, in.RemoteJID, in.Sender, in.IDs)
			if err != nil {
				return done(nil, err)
			}
			return done(newReceiptView("Mensagens marcadas como lidas", plural(len(in.IDs), "mensagem", "mensagens"), mgr.ChatName(ctx, a, in.RemoteJID), nil), nil)
		})

	// --- profile / privacy / block ---
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_group_participants", Description: "List a group's participants."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupJidArgs) (*mcp.CallToolResult, any, error) {
			a := acct(ctx)
			ps, err := mgr.GroupParticipants(ctx, a, in.GroupJID)
			if err != nil {
				return done(nil, err)
			}
			return done(participantsView{
				Kind:         kindParticipants,
				Group:        groupRef{JID: in.GroupJID, Name: mgr.ChatName(ctx, a, in.GroupJID)},
				Participants: newParticipantViews(ps),
			}, nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_fetch_profile", Description: "Fetch a number's profile (name, status and picture)."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in numberArg) (*mcp.CallToolResult, any, error) {
			a := acct(ctx)
			res, err := mgr.FetchProfile(ctx, a, in.Number)
			if err != nil {
				return done(nil, err)
			}
			return done(mgr.ProfileCard(ctx, a, in.Number, res), nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_fetch_business_profile", Description: "Fetch a number's WhatsApp Business profile."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in numberArg) (*mcp.CallToolResult, any, error) {
			res, err := mgr.FetchBusinessProfile(ctx, acct(ctx), in.Number)
			if err != nil {
				return done(nil, err)
			}
			return done(newBusinessView(res), nil)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_profile_picture_url", Description: "Get a contact's profile picture URL."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in numberArg) (*mcp.CallToolResult, any, error) {
			res, err := mgr.ProfilePictureURL(ctx, acct(ctx), in.Number)
			return done(map[string]string{"url": res}, err)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_get_privacy", Description: "Get the account's privacy settings."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.GetPrivacy(ctx, acct(ctx))
			if err != nil {
				return done(nil, err)
			}
			return done(newPrivacyView(res), nil)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_update_profile_status", Description: "Set the account's About/status text."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in statusArg) (*mcp.CallToolResult, any, error) {
			err := mgr.UpdateProfileStatus(ctx, acct(ctx), in.Status)
			return done(map[string]bool{"ok": err == nil}, err)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_block_contact", Description: "Block or unblock a number (status: block|unblock)."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, in blockArgs) (*mcp.CallToolResult, any, error) {
			a := acct(ctx)
			err := mgr.BlockContact(ctx, a, in.Number, in.Status)
			if err != nil {
				return done(nil, err)
			}
			action := "Contato bloqueado"
			if in.Status == "unblock" {
				action = "Contato desbloqueado"
			}
			return done(newReceiptView(action, mgr.ChatName(ctx, a, in.Number), in.Number, nil), nil)
		})

	// --- account / lifecycle ---
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_instance_state", Description: "Connection/login state of the account."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			a := acct(ctx)
			if _, err := mgr.InstanceState(ctx, a); err != nil {
				return done(nil, err)
			}
			sum := accountOf(ctx, mgr)
			if sum == nil {
				return done(nil, fmt.Errorf("account not connected"))
			}
			return done(accountView{Kind: kindAccount, Account: *sum}, nil)
		})
	mcp.AddTool(s, withUI(&mcp.Tool{Name: "whatsapp_instance_list", Description: "List the caller's own linked account JID(s)."}),
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			all, err := mgr.ListDevices(ctx)
			// Multi-tenant: never leak other tenants' JIDs — scope to the caller.
			if a := acct(ctx); a != "" {
				mine := []string{}
				for _, j := range all {
					if strings.HasPrefix(j, a+":") || strings.HasPrefix(j, a+"@") {
						mine = append(mine, j)
					}
				}
				return done(devicesView{Kind: kindDevices, Devices: mine}, err)
			}
			return done(devicesView{Kind: kindDevices, Devices: all}, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_instance_logout", Description: "Unlink (log out) the account's device."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			err := mgr.Logout(ctx, acct(ctx))
			return done(map[string]bool{"ok": err == nil}, err)
		})

	return s
}

// done builds a JSON text result (or an error result).
func done(v any, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}

// clamp returns v bounded to (0, max], defaulting to def when v is unset.
func clamp(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func participantActionLabel(action string) string {
	switch action {
	case "add":
		return "Participantes adicionados"
	case "remove":
		return "Participantes removidos"
	case "promote":
		return "Participantes promovidos a admin"
	case "demote":
		return "Admins rebaixados"
	default:
		return "Participantes atualizados"
	}
}

// accountOf builds the account summary the cards show in their header.
func accountOf(ctx context.Context, mgr *wa.Manager) *accountSummary {
	a := acct(ctx)
	st, err := mgr.InstanceState(ctx, a)
	if err != nil {
		return nil
	}
	jid, _ := st["jid"].(string)
	connected, _ := st["connected"].(bool)
	loggedIn, _ := st["loggedIn"].(bool)
	sum := &accountSummary{JID: jid, Connected: connected, LoggedIn: loggedIn, PushName: mgr.PushName(a)}
	if self := mgr.SelfJID(a); !self.IsEmpty() {
		sum.Number = self.ToNonAD().User
	}
	return sum
}

// acct resolves the caller's WhatsApp account from the auth context (HTTP
// per-user via the bearer JWT sub); empty string means the default (stdio).
func acct(ctx context.Context) string { return oauth.SubFromContext(ctx) }

// ---- argument structs ----

type emptyArgs struct{}

type sendTextArgs struct {
	Number string `json:"number" jsonschema:"WhatsApp number or JID"`
	Text   string `json:"text" jsonschema:"Message text"`
}

type sendButtonsArgs struct {
	Number       string      `json:"number" jsonschema:"WhatsApp number or JID"`
	Text         string      `json:"text" jsonschema:"Message body"`
	Title        string      `json:"title,omitempty" jsonschema:"Optional bold header"`
	Footer       string      `json:"footer,omitempty" jsonschema:"Optional small footer text"`
	Buttons      []wa.Button `json:"buttons" jsonschema:"1-10 buttons; type is quick_reply|url|call|copy; quick_reply takes id, url takes url, call takes phone, copy takes code"`
	Flavor       string      `json:"flavor,omitempty" jsonschema:"Stanza shape: mixed (default) or full; try full if buttons don't render"`
	FallbackText bool        `json:"fallbackText,omitempty" jsonschema:"If WhatsApp refuses the buttons, send the same content as plain text"`
}

type sendFormArgs struct {
	Number string `json:"number" jsonschema:"WhatsApp number or JID"`
	wa.FormSpec
}

type getFormArgs struct {
	FormID string `json:"formId" jsonschema:"form_id returned by whatsapp_send_form"`
}

type formSentView struct {
	wa.FormSent
	To string `json:"to"`
}

type sendFlowArgs struct {
	Number       string         `json:"number" jsonschema:"WhatsApp number or JID"`
	Text         string         `json:"text" jsonschema:"Message body"`
	Title        string         `json:"title,omitempty" jsonschema:"Optional bold header"`
	Footer       string         `json:"footer,omitempty" jsonschema:"Optional small footer text"`
	CTA          string         `json:"cta,omitempty" jsonschema:"Button label (default Abrir)"`
	FlowID       string         `json:"flowId,omitempty" jsonschema:"Published flow id"`
	FlowToken    string         `json:"flowToken,omitempty" jsonschema:"Opaque token echoed back with the answers"`
	Mode         string         `json:"mode,omitempty" jsonschema:"published (default) or draft"`
	Action       string         `json:"action,omitempty" jsonschema:"navigate (default) or data_exchange"`
	Screen       string         `json:"screen,omitempty" jsonschema:"First screen id (navigate)"`
	Data         map[string]any `json:"data,omitempty" jsonschema:"Initial screen data (navigate)"`
	FlowJSON     string         `json:"flowJson,omitempty" jsonschema:"Raw flow definition to embed (unverified)"`
	Extra        map[string]any `json:"extra,omitempty" jsonschema:"Extra button params merged as-is (e.g. flow_metadata)"`
	DummyReply   bool           `json:"dummyReply,omitempty" jsonschema:"Put a quick_reply button before the flow button"`
	Flavor       string         `json:"flavor,omitempty" jsonschema:"Stanza shape: mixed (default) or full"`
	FallbackText bool           `json:"fallbackText,omitempty" jsonschema:"If refused, send the body as plain text"`
}

type sendListArgs struct {
	Number       string           `json:"number" jsonschema:"WhatsApp number or JID"`
	Text         string           `json:"text" jsonschema:"Message body"`
	ButtonText   string           `json:"buttonText" jsonschema:"Label of the button that opens the list"`
	Title        string           `json:"title,omitempty" jsonschema:"Optional bold header"`
	Footer       string           `json:"footer,omitempty" jsonschema:"Optional small footer text"`
	Sections     []wa.ListSection `json:"sections" jsonschema:"1-10 sections, each with a title and 1-10 rows {id, title, description}"`
	FallbackText bool             `json:"fallbackText,omitempty" jsonschema:"If WhatsApp refuses the list, send the options as a numbered plain-text list"`
}

type sendMediaArgs struct {
	Number    string `json:"number"`
	Mediatype string `json:"mediatype" jsonschema:"image|video|document"`
	Media     string `json:"media" jsonschema:"URL or base64"`
	Caption   string `json:"caption,omitempty"`
	Mimetype  string `json:"mimetype,omitempty"`
	FileName  string `json:"fileName,omitempty"`
}

type sendAudioArgs struct {
	Number string `json:"number"`
	Audio  string `json:"audio" jsonschema:"URL or base64 (ogg/opus)"`
}

type msgKey struct {
	RemoteJID string `json:"remoteJid"`
	FromMe    bool   `json:"fromMe,omitempty"`
	ID        string `json:"id"`
}

type reactionArgs struct {
	Key      msgKey `json:"key"`
	Reaction string `json:"reaction" jsonschema:"Emoji, empty string removes"`
}

type checkNumbersArgs struct {
	Numbers []string `json:"numbers"`
}

type findContactsArgs struct {
	Query string `json:"query,omitempty" jsonschema:"Search contacts by name or number (case-insensitive substring); omit to list the first page"`
	Limit int    `json:"limit,omitempty" jsonschema:"Max contacts to return (default 50, max 500)"`
}

type findGroupsArgs struct {
	Query string `json:"query,omitempty" jsonschema:"Filter groups by name or topic (case-insensitive substring)"`
	Limit int    `json:"limit,omitempty" jsonschema:"Max groups to return (default 50, max 500)"`
}

type listChatsArgs struct {
	Limit int `json:"limit,omitempty"`
}

type listMessagesArgs struct {
	RemoteJID string `json:"remoteJid"`
	Limit     int    `json:"limit,omitempty"`
}

type downloadMediaArgs struct {
	ID        string `json:"id" jsonschema:"Message id to download media from (from find_messages)"`
	RemoteJID string `json:"remoteJid,omitempty" jsonschema:"Optional chat JID for context"`
}

type groupJidArgs struct {
	GroupJID string `json:"groupJid"`
}

type groupCreateArgs struct {
	Subject      string   `json:"subject"`
	Participants []string `json:"participants"`
}

type groupParticipantsArgs struct {
	GroupJID     string   `json:"groupJid"`
	Action       string   `json:"action" jsonschema:"add|remove|promote|demote"`
	Participants []string `json:"participants"`
}

type groupSubjectArgs struct {
	GroupJID string `json:"groupJid"`
	Subject  string `json:"subject"`
}

type sendStickerArgs struct {
	Number  string `json:"number"`
	Sticker string `json:"sticker" jsonschema:"URL or base64 webp"`
}

type sendLocationArgs struct {
	Number    string  `json:"number"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Name      string  `json:"name,omitempty"`
	Address   string  `json:"address,omitempty"`
}

type sendContactArgs struct {
	Number      string `json:"number"`
	FullName    string `json:"fullName"`
	PhoneNumber string `json:"phoneNumber"`
}

type sendPollArgs struct {
	Number          string   `json:"number"`
	Name            string   `json:"name"`
	SelectableCount int      `json:"selectableCount"`
	Values          []string `json:"values"`
}

type deleteMessageArgs struct {
	RemoteJID string `json:"remoteJid"`
	ID        string `json:"id"`
}

type updateMessageArgs struct {
	RemoteJID string `json:"remoteJid"`
	ID        string `json:"id"`
	Text      string `json:"text"`
}

type setPresenceArgs struct {
	Presence string `json:"presence" jsonschema:"available|unavailable"`
}

type chatPresenceArgs struct {
	Number   string `json:"number"`
	Presence string `json:"presence" jsonschema:"composing|recording|paused"`
}

type markReadArgs struct {
	RemoteJID string   `json:"remoteJid"`
	Sender    string   `json:"sender,omitempty"`
	IDs       []string `json:"ids"`
}

type numberArg struct {
	Number string `json:"number"`
}

type statusArg struct {
	Status string `json:"status"`
}

type blockArgs struct {
	Number string `json:"number"`
	Status string `json:"status" jsonschema:"block|unblock"`
}
