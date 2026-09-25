package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/types"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

// The bundle must be a self-contained HTML document: the host renders it in a
// sandboxed iframe whose CSP blocks external scripts, styles and fonts.
func TestAppHTMLIsSelfContained(t *testing.T) {
	if len(appHTML) < 1000 {
		t.Fatalf("app bundle looks empty (%d bytes) — run the app build", len(appHTML))
	}
	if !strings.Contains(appHTML, "<!doctype html>") {
		t.Error("bundle is not an HTML document")
	}
	// Declaring both color schemes is what drops the iframe's opaque backdrop.
	if !strings.Contains(appHTML, `name="color-scheme" content="light dark"`) {
		t.Error("missing color-scheme meta: the card would paint an opaque backdrop")
	}
	for _, bad := range []string{`<script src="http`, `<link rel="stylesheet" href="http`, `@import url(http`} {
		if strings.Contains(appHTML, bad) {
			t.Errorf("bundle references an external asset (%q): the host CSP blocks it", bad)
		}
	}
}

func TestAppResourceMetaMarksBorderlessAndAllowsHostFonts(t *testing.T) {
	ui, ok := appResourceMeta["ui"].(map[string]any)
	if !ok {
		t.Fatal("resource _meta.ui missing")
	}
	if ui["prefersBorder"] != false {
		t.Error("prefersBorder must be false: the cards draw their own surface")
	}
	csp, ok := ui["csp"].(map[string]any)
	if !ok {
		t.Fatal("csp missing")
	}
	domains, _ := csp["resourceDomains"].([]string)
	if !contains(domains, "https://assets.claude.ai") {
		t.Error("assets.claude.ai must be allowed or applyHostFonts cannot load Anthropic Sans")
	}
}

func TestToolUIMetaPointsAtTheResource(t *testing.T) {
	ui, ok := uiMeta["ui"].(map[string]any)
	if !ok {
		t.Fatal("tool _meta.ui missing")
	}
	if ui["resourceUri"] != appURI {
		t.Errorf("tool points at %v, want %s", ui["resourceUri"], appURI)
	}
}

// Every card is chosen by the `kind` discriminator, so each view must carry it.
func TestViewsCarryTheirKind(t *testing.T) {
	cases := []struct {
		want string
		v    any
	}{
		{kindChats, newChatsView(nil, nil, nil)},
		{kindMessages, newMessagesView(chatRef{JID: "x@s.whatsapp.net"}, nil, nil, false)},
		{kindNumbers, newNumbersView(nil)},
		{kindSent, newSentView("id", "5521@s.whatsapp.net", "text", "oi")},
		{kindReceipt, newReceiptView("feito", "", "", nil)},
		{kindPrivacy, newPrivacyView(types.PrivacySettings{})},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.v)
		if err != nil {
			t.Fatalf("marshal %T: %v", c.v, err)
		}
		var got struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("unmarshal %T: %v", c.v, err)
		}
		if got.Kind != c.want {
			t.Errorf("%T kind = %q, want %q", c.v, got.Kind, c.want)
		}
	}
}

func TestChatKindClassification(t *testing.T) {
	for jid, want := range map[string]string{
		"5521968865678@s.whatsapp.net": "dm",
		"120363@g.us":                  "group",
		"120363@newsletter":            "newsletter",
		"status@broadcast":             "status",
		"199887@lid":                   "dm",
	} {
		if got := chatKindOf(jid); got != want {
			t.Errorf("chatKindOf(%q) = %q, want %q", jid, got, want)
		}
	}
}

// The chat list carries a preview line so the card is readable without opening
// each conversation.
func TestChatsViewMergesPreviews(t *testing.T) {
	chats := []appstore.Chat{{JID: "a@s.whatsapp.net", Name: "Ana", LastTS: 100}, {JID: "b@g.us", Name: "Time", LastTS: 90}}
	previews := map[string]appstore.Message{
		"a@s.whatsapp.net": {Body: "oi, tudo bem?", FromMe: true},
		"b@g.us":           {MediaType: "image"},
	}
	v := newChatsView(chats, previews, nil)
	if v.Total != 2 {
		t.Fatalf("total = %d, want 2", v.Total)
	}
	if v.Chats[0].Preview != "oi, tudo bem?" || !v.Chats[0].FromMe {
		t.Errorf("dm preview not merged: %+v", v.Chats[0])
	}
	if v.Chats[1].MediaType != "image" || v.Chats[1].Kind != "group" {
		t.Errorf("group preview not merged: %+v", v.Chats[1])
	}
}

// A media message must be flagged so the card offers the download affordance.
func TestMessagesViewFlagsMediaAndNamesSenders(t *testing.T) {
	msgs := []appstore.Message{
		{ID: "1", SenderJID: "x@s.whatsapp.net", Body: "texto"},
		{ID: "2", SenderJID: "x@s.whatsapp.net", MediaType: "audio"},
	}
	v := newMessagesView(chatRef{JID: "g@g.us", Kind: "group"}, msgs, map[string]string{"x@s.whatsapp.net": "Bruno"}, true)
	if v.Msgs[0].HasMedia {
		t.Error("text message must not be flagged as media")
	}
	if !v.Msgs[1].HasMedia {
		t.Error("audio message must be flagged so the card can download it")
	}
	if v.Msgs[0].SenderName != "Bruno" {
		t.Errorf("sender name = %q, want Bruno", v.Msgs[0].SenderName)
	}
	if !v.HasMore {
		t.Error("has_more should propagate so the card can offer 'load more'")
	}
}

func TestTruncateClipsOnRunes(t *testing.T) {
	if got := truncate("ação", 10); got != "ação" {
		t.Errorf("short string changed: %q", got)
	}
	got := truncate("açãoçãoção", 4)
	if []rune(got)[len([]rune(got))-1] != '…' {
		t.Errorf("truncated value should end with an ellipsis, got %q", got)
	}
	if len([]rune(got)) > 5 {
		t.Errorf("truncate cut on bytes, not runes: %q", got)
	}
}

func TestClampBoundsListSizes(t *testing.T) {
	if got := clamp(0, 50, 500); got != 50 {
		t.Errorf("unset limit = %d, want the default 50", got)
	}
	if got := clamp(9999, 50, 500); got != 500 {
		t.Errorf("oversized limit = %d, want the 500 cap", got)
	}
	if got := clamp(10, 50, 500); got != 10 {
		t.Errorf("explicit limit = %d, want 10", got)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
