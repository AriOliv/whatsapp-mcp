// Package mcpserver builds the MCP server and registers the WhatsApp tools.
// Tools are named whatsapp_* and map directly to whatsmeow calls — there is no
// Evolution API in the loop.
package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AriOliv/whatsapp-mcp/internal/oauth"
	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

// Build wires all tools against the whatsmeow manager and returns the server.
func Build(mgr *wa.Manager) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "whatsapp-mcp", Version: "2.0.0-whatsmeow"}, nil)

	// --- messaging ---
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_send_text", Description: "Send a WhatsApp text message."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendTextArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendText(ctx, acct(ctx), in.Number, in.Text)
			return done(map[string]string{"id": id}, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_send_media", Description: "Send an image/video/document (URL or base64)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendMediaArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendMedia(ctx, acct(ctx), in.Number, in.Mediatype, in.Media, in.Caption, in.Mimetype, in.FileName)
			return done(map[string]string{"id": id}, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_send_audio", Description: "Send a voice/audio message (URL or base64)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendAudioArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendMedia(ctx, acct(ctx), in.Number, "audio", in.Audio, "", "", "")
			return done(map[string]string{"id": id}, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_send_reaction", Description: "React to a message with an emoji (empty removes)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in reactionArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.React(ctx, acct(ctx), in.Key.RemoteJID, in.Key.RemoteJID, in.Key.ID, in.Reaction)
			return done(map[string]string{"id": id}, err)
		})

	// --- contacts / numbers ---
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_check_numbers", Description: "Check which numbers are registered on WhatsApp."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in checkNumbersArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.CheckNumbers(ctx, acct(ctx), in.Numbers)
			return done(res, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_find_contacts", Description: "List stored contacts for the account."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.Contacts(ctx, acct(ctx))
			return done(res, err)
		})

	// --- chats & messages (our own store) ---
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_find_chats", Description: "List recent conversations (from local history)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listChatsArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.ListChats(ctx, acct(ctx), in.Limit)
			return done(res, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_find_messages", Description: "List messages in a chat (from local history)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listMessagesArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.ListMessages(ctx, acct(ctx), in.RemoteJID, in.Limit)
			return done(res, err)
		})

	// --- groups ---
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_group_fetch_all", Description: "Fetch all joined groups."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.Groups(ctx, acct(ctx))
			return done(res, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_group_info", Description: "Get info for one group by JID."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupJidArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.GroupInfo(ctx, acct(ctx), in.GroupJID)
			return done(res, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_group_create", Description: "Create a group with participants."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupCreateArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.CreateGroup(ctx, acct(ctx), in.Subject, in.Participants)
			return done(res, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_group_invite_code", Description: "Get a group's invite link."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupJidArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.InviteLink(ctx, acct(ctx), in.GroupJID, false)
			return done(map[string]string{"inviteLink": res}, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_group_update_participant", Description: "Add/remove/promote/demote group members."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupParticipantsArgs) (*mcp.CallToolResult, any, error) {
			err := mgr.UpdateParticipants(ctx, acct(ctx), in.GroupJID, in.Action, in.Participants)
			return done(map[string]bool{"ok": err == nil}, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_group_update_subject", Description: "Change a group's subject/name."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupSubjectArgs) (*mcp.CallToolResult, any, error) {
			err := mgr.SetGroupName(ctx, acct(ctx), in.GroupJID, in.Subject)
			return done(map[string]bool{"ok": err == nil}, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_group_leave", Description: "Leave a group."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupJidArgs) (*mcp.CallToolResult, any, error) {
			err := mgr.LeaveGroup(ctx, acct(ctx), in.GroupJID)
			return done(map[string]bool{"ok": err == nil}, err)
		})

	// --- messaging extras ---
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_send_sticker", Description: "Send a sticker (webp, URL or base64)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendStickerArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendSticker(ctx, acct(ctx), in.Number, in.Sticker)
			return done(map[string]string{"id": id}, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_send_location", Description: "Send a location pin."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendLocationArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendLocation(ctx, acct(ctx), in.Number, in.Latitude, in.Longitude, in.Name, in.Address)
			return done(map[string]string{"id": id}, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_send_contact", Description: "Send a contact card."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendContactArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendContact(ctx, acct(ctx), in.Number, in.FullName, in.PhoneNumber)
			return done(map[string]string{"id": id}, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_send_poll", Description: "Send a poll."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendPollArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendPoll(ctx, acct(ctx), in.Number, in.Name, in.Values, in.SelectableCount)
			return done(map[string]string{"id": id}, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_delete_message", Description: "Delete a message for everyone (revoke)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in deleteMessageArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.DeleteMessage(ctx, acct(ctx), in.RemoteJID, in.ID)
			return done(map[string]string{"id": id}, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_update_message", Description: "Edit a sent text message."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updateMessageArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.EditMessage(ctx, acct(ctx), in.RemoteJID, in.ID, in.Text)
			return done(map[string]string{"id": id}, err)
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
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_mark_read", Description: "Mark messages as read in a chat."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in markReadArgs) (*mcp.CallToolResult, any, error) {
			err := mgr.MarkRead(ctx, acct(ctx), in.RemoteJID, in.Sender, in.IDs)
			return done(map[string]bool{"ok": err == nil}, err)
		})

	// --- profile / privacy / block ---
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_group_participants", Description: "List a group's participants."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupJidArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.GroupParticipants(ctx, acct(ctx), in.GroupJID)
			return done(res, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_fetch_profile", Description: "Fetch a number's profile (name/status)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in numberArg) (*mcp.CallToolResult, any, error) {
			res, err := mgr.FetchProfile(ctx, acct(ctx), in.Number)
			return done(res, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_fetch_business_profile", Description: "Fetch a number's WhatsApp Business profile."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in numberArg) (*mcp.CallToolResult, any, error) {
			res, err := mgr.FetchBusinessProfile(ctx, acct(ctx), in.Number)
			return done(res, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_profile_picture_url", Description: "Get a contact's profile picture URL."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in numberArg) (*mcp.CallToolResult, any, error) {
			res, err := mgr.ProfilePictureURL(ctx, acct(ctx), in.Number)
			return done(map[string]string{"url": res}, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_get_privacy", Description: "Get the account's privacy settings."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.GetPrivacy(ctx, acct(ctx))
			return done(res, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_update_profile_status", Description: "Set the account's About/status text."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in statusArg) (*mcp.CallToolResult, any, error) {
			err := mgr.UpdateProfileStatus(ctx, acct(ctx), in.Status)
			return done(map[string]bool{"ok": err == nil}, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_block_contact", Description: "Block or unblock a number (status: block|unblock)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in blockArgs) (*mcp.CallToolResult, any, error) {
			err := mgr.BlockContact(ctx, acct(ctx), in.Number, in.Status)
			return done(map[string]bool{"ok": err == nil}, err)
		})

	// --- account / lifecycle ---
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_instance_state", Description: "Connection/login state of the account."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.InstanceState(ctx, acct(ctx))
			return done(res, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_instance_list", Description: "List linked account JIDs."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.ListDevices(ctx)
			return done(res, err)
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

// acct resolves the caller's WhatsApp account from the auth context (HTTP
// per-user via the bearer JWT sub); empty string means the default (stdio).
func acct(ctx context.Context) string { return oauth.SubFromContext(ctx) }

// ---- argument structs ----

type emptyArgs struct{}

type sendTextArgs struct {
	Number string `json:"number" jsonschema:"WhatsApp number or JID"`
	Text   string `json:"text" jsonschema:"Message text"`
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

type listChatsArgs struct {
	Limit int `json:"limit,omitempty"`
}

type listMessagesArgs struct {
	RemoteJID string `json:"remoteJid"`
	Limit     int    `json:"limit,omitempty"`
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
