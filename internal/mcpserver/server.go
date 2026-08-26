// Package mcpserver builds the MCP server and registers the WhatsApp tools.
// Tool names keep the evo_* prefix from the original server so the swap is a
// drop-in for existing Studio connections / agents (the backend changes, the
// tool contract does not).
package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

// Build wires all tools against the whatsmeow manager and returns the server.
func Build(mgr *wa.Manager) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "whatsapp-mcp", Version: "2.0.0-whatsmeow"}, nil)

	// --- messaging ---
	mcp.AddTool(s, &mcp.Tool{Name: "evo_send_text", Description: "Send a WhatsApp text message."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendTextArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendText(ctx, "", in.Number, in.Text)
			return done(map[string]string{"id": id}, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "evo_send_media", Description: "Send an image/video/document (URL or base64)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendMediaArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendMedia(ctx, "", in.Number, in.Mediatype, in.Media, in.Caption, in.Mimetype, in.FileName)
			return done(map[string]string{"id": id}, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "evo_send_audio", Description: "Send a voice/audio message (URL or base64)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendAudioArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.SendMedia(ctx, "", in.Number, "audio", in.Audio, "", "", "")
			return done(map[string]string{"id": id}, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "evo_send_reaction", Description: "React to a message with an emoji (empty removes)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in reactionArgs) (*mcp.CallToolResult, any, error) {
			id, err := mgr.React(ctx, "", in.Key.RemoteJID, in.Key.RemoteJID, in.Key.ID, in.Reaction)
			return done(map[string]string{"id": id}, err)
		})

	// --- contacts / numbers ---
	mcp.AddTool(s, &mcp.Tool{Name: "evo_check_numbers", Description: "Check which numbers are registered on WhatsApp."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in checkNumbersArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.CheckNumbers(ctx, "", in.Numbers)
			return done(res, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "evo_find_contacts", Description: "List stored contacts for the account."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.Contacts(ctx, "")
			return done(res, err)
		})

	// --- chats & messages (our own store) ---
	mcp.AddTool(s, &mcp.Tool{Name: "evo_find_chats", Description: "List recent conversations (from local history)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listChatsArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.ListChats(ctx, "", in.Limit)
			return done(res, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "evo_find_messages", Description: "List messages in a chat (from local history)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listMessagesArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.ListMessages(ctx, "", in.RemoteJID, in.Limit)
			return done(res, err)
		})

	// --- groups ---
	mcp.AddTool(s, &mcp.Tool{Name: "evo_group_fetch_all", Description: "Fetch all joined groups."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.Groups(ctx, "")
			return done(res, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "evo_group_info", Description: "Get info for one group by JID."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupJidArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.GroupInfo(ctx, "", in.GroupJID)
			return done(res, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "evo_group_create", Description: "Create a group with participants."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupCreateArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.CreateGroup(ctx, "", in.Subject, in.Participants)
			return done(res, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "evo_group_invite_code", Description: "Get a group's invite link."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupJidArgs) (*mcp.CallToolResult, any, error) {
			res, err := mgr.InviteLink(ctx, "", in.GroupJID, false)
			return done(map[string]string{"inviteLink": res}, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "evo_group_update_participant", Description: "Add/remove/promote/demote group members."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupParticipantsArgs) (*mcp.CallToolResult, any, error) {
			err := mgr.UpdateParticipants(ctx, "", in.GroupJID, in.Action, in.Participants)
			return done(map[string]bool{"ok": err == nil}, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "evo_group_update_subject", Description: "Change a group's subject/name."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupSubjectArgs) (*mcp.CallToolResult, any, error) {
			err := mgr.SetGroupName(ctx, "", in.GroupJID, in.Subject)
			return done(map[string]bool{"ok": err == nil}, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "evo_group_leave", Description: "Leave a group."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in groupJidArgs) (*mcp.CallToolResult, any, error) {
			err := mgr.LeaveGroup(ctx, "", in.GroupJID)
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

// ---- argument structs (param names mirror the original evo_* contract) ----

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
