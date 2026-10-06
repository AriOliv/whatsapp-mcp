package wa

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func TestBuildButtonsMessage(t *testing.T) {
	msg, err := buildButtonsMessage(ButtonsSpec{
		Text:   "Escolha:",
		Footer: "rodapé",
		Buttons: []Button{
			{Type: "quick_reply", Text: "Sim", ID: "yes"},
			{Type: "url", Text: "Site", URL: "https://avenia.io"},
			{Type: "call", Text: "Ligar", Phone: "+55 11 99999-0000"},
			{Type: "copy", Text: "Copiar PIX", Code: "chave-pix"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	im := msg.GetInteractiveMessage()
	if im == nil {
		t.Fatal("want a top-level InteractiveMessage")
	}
	if msg.ViewOnceMessage != nil || msg.DocumentWithCaptionMessage != nil {
		t.Fatal("buttons must not be wrapped")
	}
	if im.GetBody().GetText() != "Escolha:" || im.GetFooter().GetText() != "rodapé" {
		t.Fatalf("body/footer: %q / %q", im.GetBody().GetText(), im.GetFooter().GetText())
	}
	if im.Header != nil {
		t.Fatal("header must be omitted when there is no title (iOS drops empty headers)")
	}
	nf := im.GetNativeFlowMessage()
	if nf.GetMessageVersion() != 1 {
		t.Fatalf("message version = %d", nf.GetMessageVersion())
	}
	want := []struct{ name, key, val string }{
		{"quick_reply", "id", "yes"},
		{"cta_url", "url", "https://avenia.io"},
		{"cta_call", "phone_number", "+55 11 99999-0000"},
		{"cta_copy", "copy_code", "chave-pix"},
	}
	if len(nf.GetButtons()) != len(want) {
		t.Fatalf("got %d buttons", len(nf.GetButtons()))
	}
	for i, w := range want {
		b := nf.GetButtons()[i]
		if b.GetName() != w.name {
			t.Errorf("button %d name = %q, want %q", i, b.GetName(), w.name)
		}
		var p map[string]string
		if err := json.Unmarshal([]byte(b.GetButtonParamsJSON()), &p); err != nil {
			t.Fatalf("button %d params: %v", i, err)
		}
		if p[w.key] != w.val || p["display_text"] == "" {
			t.Errorf("button %d params = %v", i, p)
		}
	}
}

func TestBuildButtonsMessageTitle(t *testing.T) {
	msg, err := buildButtonsMessage(ButtonsSpec{Text: "x", Title: "Título", Buttons: []Button{{Type: "quick_reply", Text: "Ok"}}})
	if err != nil {
		t.Fatal(err)
	}
	h := msg.GetInteractiveMessage().GetHeader()
	if h.GetTitle() != "Título" || h.Subtitle != nil {
		t.Fatalf("header = %+v", h)
	}
	// quick_reply without id falls back to its label
	var p map[string]string
	_ = json.Unmarshal([]byte(msg.GetInteractiveMessage().GetNativeFlowMessage().GetButtons()[0].GetButtonParamsJSON()), &p)
	if p["id"] != "Ok" {
		t.Fatalf("id = %q", p["id"])
	}
}

func TestBuildButtonsMessageErrors(t *testing.T) {
	qr := func(id string) Button { return Button{Type: "quick_reply", Text: id, ID: id} }
	cases := map[string]ButtonsSpec{
		"no text":        {Buttons: []Button{qr("a")}},
		"no buttons":     {Text: "x"},
		"too many quick": {Text: "x", Buttons: []Button{qr("a"), qr("b"), qr("c"), qr("d")}},
		"duplicate id":   {Text: "x", Buttons: []Button{qr("a"), qr("a")}},
		"http url":       {Text: "x", Buttons: []Button{{Type: "url", Text: "s", URL: "http://x.com"}}},
		"call no phone":  {Text: "x", Buttons: []Button{{Type: "call", Text: "c"}}},
		"copy no code":   {Text: "x", Buttons: []Button{{Type: "copy", Text: "c"}}},
		"unknown type":   {Text: "x", Buttons: []Button{{Type: "flow", Text: "c"}}},
		"empty label":    {Text: "x", Buttons: []Button{{Type: "quick_reply", ID: "a"}}},
		"over ten buttons": {Text: "x", Buttons: func() []Button {
			var bs []Button
			for i := 0; i < 11; i++ {
				bs = append(bs, Button{Type: "url", Text: "s", URL: "https://x.com"})
			}
			return bs
		}()},
	}
	for name, spec := range cases {
		if _, err := buildButtonsMessage(spec); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("%s: want ErrInvalidSpec, got %v", name, err)
		}
	}
}

func TestBuildListMessage(t *testing.T) {
	msg, err := buildListMessage(ListSpec{
		Text: "Escolha um produto", ButtonText: "Ver opções", Title: "Menu",
		Sections: []ListSection{
			{Title: "Contas", Rows: []ListRow{{ID: "pf", Title: "Pessoa física", Description: "CPF"}, {ID: "pj", Title: "Empresa"}}},
			{Title: "Outros", Rows: []ListRow{{Title: "Falar com atendente"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	lm := msg.GetListMessage()
	if lm.GetListType() != waE2E.ListMessage_SINGLE_SELECT {
		t.Fatalf("list type = %v", lm.GetListType())
	}
	if lm.GetDescription() != "Escolha um produto" || lm.GetButtonText() != "Ver opções" || lm.GetTitle() != "Menu" {
		t.Fatalf("list = %+v", lm)
	}
	if len(lm.GetSections()) != 2 || lm.GetSections()[1].GetRows()[0].GetRowID() != "Falar com atendente" {
		t.Fatalf("sections = %+v", lm.GetSections())
	}
	if lm.GetSections()[0].GetRows()[1].Description != nil {
		t.Fatal("empty description must be omitted")
	}
}

func TestBuildListMessageErrors(t *testing.T) {
	row := ListRow{ID: "a", Title: "A"}
	cases := map[string]ListSpec{
		"no text":     {ButtonText: "b", Sections: []ListSection{{Rows: []ListRow{row}}}},
		"no button":   {Text: "x", Sections: []ListSection{{Rows: []ListRow{row}}}},
		"no sections": {Text: "x", ButtonText: "b"},
		"empty rows":  {Text: "x", ButtonText: "b", Sections: []ListSection{{Title: "s"}}},
		"dup row id":  {Text: "x", ButtonText: "b", Sections: []ListSection{{Rows: []ListRow{row}}, {Rows: []ListRow{row}}}},
		"row title":   {Text: "x", ButtonText: "b", Sections: []ListSection{{Rows: []ListRow{{ID: "a"}}}}},
	}
	for name, spec := range cases {
		if _, err := buildListMessage(spec); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("%s: want ErrInvalidSpec, got %v", name, err)
		}
	}
}

func nodeTags(nodes []waBinary.Node) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.Tag)
	}
	return out
}

func TestButtonsNodes(t *testing.T) {
	now := time.Unix(1790000000, 0)
	dm := types.NewJID("5511999990000", types.DefaultUserServer)
	group := types.NewJID("123", types.GroupServer)

	nodes := buttonsNodes(dm, FlavorMixed, now)
	if got := strings.Join(nodeTags(nodes), ","); got != "biz" {
		t.Fatalf("dm nodes = %s (no <bot> node by default: it labels the message \"AI\")", got)
	}
	biz := nodes[0]
	if len(biz.Attrs) != 0 {
		t.Fatalf("mixed biz must have no attrs, got %v", biz.Attrs)
	}
	inter := biz.GetChildren()[0]
	if inter.Tag != "interactive" || inter.Attrs["type"] != "native_flow" || inter.Attrs["v"] != "1" {
		t.Fatalf("interactive = %+v", inter)
	}
	nf := inter.GetChildren()[0]
	if nf.Tag != "native_flow" || nf.Attrs["name"] != "mixed" || nf.Attrs["v"] != "9" {
		t.Fatalf("native_flow = %+v", nf)
	}

	bot := buttonsNodes(dm, FlavorBot, now)
	if got := strings.Join(nodeTags(bot), ","); got != "biz,bot" || bot[1].Attrs["biz_bot"] != "1" {
		t.Fatalf("bot flavor dm nodes = %s %v", got, bot)
	}
	if got := strings.Join(nodeTags(buttonsNodes(group, FlavorBot, now)), ","); got != "biz" {
		t.Fatalf("group nodes = %s (no bot node in groups)", got)
	}

	full := buttonsNodes(dm, FlavorFull, now)[0]
	if full.Attrs["privacy_mode_ts"] != "1790000000" || full.Attrs["actual_actors"] != "2" || full.Attrs["host_storage"] != "2" {
		t.Fatalf("full biz attrs = %v", full.Attrs)
	}
	if got := strings.Join(nodeTags(full.GetChildren()), ","); got != "interactive,quality_control" {
		t.Fatalf("full biz children = %s", got)
	}
}

func TestInteractiveReplyAndMessageText(t *testing.T) {
	cases := []struct {
		name     string
		msg      *waE2E.Message
		kind, id string
		body     string
	}{
		{
			name: "native flow quick reply",
			msg: &waE2E.Message{InteractiveResponseMessage: &waE2E.InteractiveResponseMessage{
				Body: &waE2E.InteractiveResponseMessage_Body{Text: proto.String("Sim")},
				InteractiveResponseMessage: &waE2E.InteractiveResponseMessage_NativeFlowResponseMessage_{
					NativeFlowResponseMessage: &waE2E.InteractiveResponseMessage_NativeFlowResponseMessage{
						Name: proto.String("quick_reply"), ParamsJSON: proto.String(`{"id":"yes"}`),
					}},
			}},
			kind: "button", id: "yes", body: "Sim [button:yes]",
		},
		{
			name: "list reply",
			msg: &waE2E.Message{ListResponseMessage: &waE2E.ListResponseMessage{
				Title:             proto.String("Empresa"),
				SingleSelectReply: &waE2E.ListResponseMessage_SingleSelectReply{SelectedRowID: proto.String("pj")},
			}},
			kind: "list", id: "pj", body: "Empresa [list:pj]",
		},
		{
			name: "legacy buttons reply, id equals label",
			msg: &waE2E.Message{ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{
				SelectedButtonID: proto.String("Ok"),
				Response:         &waE2E.ButtonsResponseMessage_SelectedDisplayText{SelectedDisplayText: "Ok"},
			}},
			kind: "button", id: "Ok", body: "Ok",
		},
	}
	for _, c := range cases {
		r := interactiveReply(c.msg)
		if r == nil || r.Kind != c.kind || r.ID != c.id {
			t.Errorf("%s: reply = %+v", c.name, r)
			continue
		}
		if got := messageText(c.msg); got != c.body {
			t.Errorf("%s: body = %q, want %q", c.name, got, c.body)
		}
		if mediaType(c.msg) != "" {
			t.Errorf("%s: a reply must not be classified as media", c.name)
		}
	}
	if interactiveReply(&waE2E.Message{Conversation: proto.String("oi")}) != nil {
		t.Error("plain text is not a reply")
	}
	// our own sent echo keeps its body text
	sent, _ := buildButtonsMessage(ButtonsSpec{Text: "Escolha:", Buttons: []Button{{Type: "quick_reply", Text: "A"}}})
	if messageText(sent) != "Escolha:" {
		t.Errorf("sent buttons body = %q", messageText(sent))
	}
}

func TestFallbackText(t *testing.T) {
	got := buttonsFallbackText(ButtonsSpec{Title: "T", Text: "Escolha:", Buttons: []Button{
		{Type: "quick_reply", Text: "Sim"}, {Type: "quick_reply", Text: "Não"}, {Type: "url", Text: "Site", URL: "https://x.com"},
	}})
	for _, want := range []string{"*T*", "Escolha:", "1. Sim", "2. Não", "Site: https://x.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("buttons fallback missing %q:\n%s", want, got)
		}
	}
	got = listFallbackText(ListSpec{Text: "Menu", Sections: []ListSection{
		{Title: "A", Rows: []ListRow{{Title: "um"}}}, {Title: "B", Rows: []ListRow{{Title: "dois", Description: "d"}}},
	}})
	for _, want := range []string{"Menu", "*A*", "1. um", "2. dois — d"} {
		if !strings.Contains(got, want) {
			t.Errorf("list fallback missing %q:\n%s", want, got)
		}
	}
}
