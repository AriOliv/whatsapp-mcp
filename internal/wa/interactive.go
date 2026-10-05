package wa

// Interactive messages (buttons and lists) for a regular, non-API account.
//
// The legacy ButtonsMessage/TemplateMessage are refused or silently dropped by
// WhatsApp today. What renders (Android, iOS, Web — 2026) is a top-level
// InteractiveMessage carrying NativeFlow buttons, sent together with an extra
// <biz> stanza node that whatsmeow does not add on its own for this type. Lists
// use the classic ListMessage; whatsmeow adds their <biz><list/></biz> node.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Button kinds accepted by the send_buttons tool.
const (
	ButtonQuickReply = "quick_reply" // tap sends a reply back to us
	ButtonURL        = "url"         // opens a link
	ButtonCall       = "call"        // dials a number
	ButtonCopy       = "copy"        // copies a code (e.g. a PIX key)
)

// Stanza-node shapes for buttons. Both are what working clients send in 2026;
// which one a given account/recipient accepts has shifted over time.
const (
	FlavorMixed = "mixed" // <biz><interactive><native_flow name="mixed"/></interactive></biz> (+ <bot> in 1:1)
	FlavorFull  = "full"  // same, with the actor/storage/privacy attrs and a quality_control node
)

const (
	maxButtons      = 10
	maxQuickReplies = 3
	maxSections     = 10
	maxRows         = 10
)

// Button is one button of a buttons message.
type Button struct {
	Type  string `json:"type"`            // quick_reply | url | call | copy
	Text  string `json:"text"`            // label shown on the button
	ID    string `json:"id,omitempty"`    // quick_reply: id returned when tapped
	URL   string `json:"url,omitempty"`   // url: https link
	Phone string `json:"phone,omitempty"` // call: number to dial
	Code  string `json:"code,omitempty"`  // copy: text copied to the clipboard
}

// ButtonsSpec describes a buttons message.
type ButtonsSpec struct {
	Text    string
	Title   string
	Footer  string
	Buttons []Button
}

// ListRow is one selectable row of a list message.
type ListRow struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// ListSection groups rows under a heading.
type ListSection struct {
	Title string    `json:"title"`
	Rows  []ListRow `json:"rows"`
}

// ListSpec describes a list message.
type ListSpec struct {
	Text       string
	Title      string
	Footer     string
	ButtonText string
	Sections   []ListSection
}

// buildButtonsMessage turns a spec into a NativeFlow InteractiveMessage.
func buildButtonsMessage(spec ButtonsSpec) (*waE2E.Message, error) {
	text := strings.TrimSpace(spec.Text)
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}
	if len(spec.Buttons) == 0 || len(spec.Buttons) > maxButtons {
		return nil, fmt.Errorf("need between 1 and %d buttons, got %d", maxButtons, len(spec.Buttons))
	}
	quick := 0
	ids := map[string]bool{}
	buttons := make([]*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton, 0, len(spec.Buttons))
	for i, b := range spec.Buttons {
		label := strings.TrimSpace(b.Text)
		if label == "" {
			return nil, fmt.Errorf("button %d: text is required", i+1)
		}
		var name string
		params := map[string]string{"display_text": label}
		switch strings.ToLower(strings.TrimSpace(b.Type)) {
		case ButtonQuickReply, "reply":
			quick++
			id := strings.TrimSpace(b.ID)
			if id == "" {
				id = label
			}
			if ids[id] {
				return nil, fmt.Errorf("button %d: duplicate id %q", i+1, id)
			}
			ids[id] = true
			name, params["id"] = "quick_reply", id
		case ButtonURL:
			u := strings.TrimSpace(b.URL)
			if !strings.HasPrefix(u, "https://") {
				return nil, fmt.Errorf("button %d: url must start with https://", i+1)
			}
			name, params["url"], params["merchant_url"] = "cta_url", u, u
		case ButtonCall:
			phone := strings.TrimSpace(b.Phone)
			if onlyDigits(phone) == "" {
				return nil, fmt.Errorf("button %d: phone is required", i+1)
			}
			name, params["phone_number"] = "cta_call", phone
		case ButtonCopy:
			if strings.TrimSpace(b.Code) == "" {
				return nil, fmt.Errorf("button %d: code is required", i+1)
			}
			name, params["copy_code"] = "cta_copy", b.Code
		default:
			return nil, fmt.Errorf("button %d: unknown type %q (use quick_reply, url, call or copy)", i+1, b.Type)
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		buttons = append(buttons, &waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
			Name:             proto.String(name),
			ButtonParamsJSON: proto.String(string(raw)),
		})
	}
	if quick > maxQuickReplies {
		return nil, fmt.Errorf("at most %d quick_reply buttons, got %d", maxQuickReplies, quick)
	}

	im := &waE2E.InteractiveMessage{
		Body: &waE2E.InteractiveMessage_Body{Text: proto.String(text)},
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
				Buttons:        buttons,
				MessageVersion: proto.Int32(1),
			},
		},
	}
	// iOS drops the whole message when a header field is present but empty, so
	// only set the header when there is a title.
	if t := strings.TrimSpace(spec.Title); t != "" {
		im.Header = &waE2E.InteractiveMessage_Header{Title: proto.String(t), HasMediaAttachment: proto.Bool(false)}
	}
	if f := strings.TrimSpace(spec.Footer); f != "" {
		im.Footer = &waE2E.InteractiveMessage_Footer{Text: proto.String(f)}
	}
	return &waE2E.Message{InteractiveMessage: im}, nil
}

// buildListMessage turns a spec into a single-select ListMessage.
func buildListMessage(spec ListSpec) (*waE2E.Message, error) {
	text := strings.TrimSpace(spec.Text)
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}
	buttonText := strings.TrimSpace(spec.ButtonText)
	if buttonText == "" {
		return nil, fmt.Errorf("buttonText is required")
	}
	if len(spec.Sections) == 0 || len(spec.Sections) > maxSections {
		return nil, fmt.Errorf("need between 1 and %d sections, got %d", maxSections, len(spec.Sections))
	}
	ids := map[string]bool{}
	sections := make([]*waE2E.ListMessage_Section, 0, len(spec.Sections))
	for i, s := range spec.Sections {
		if len(s.Rows) == 0 || len(s.Rows) > maxRows {
			return nil, fmt.Errorf("section %d: need between 1 and %d rows, got %d", i+1, maxRows, len(s.Rows))
		}
		rows := make([]*waE2E.ListMessage_Row, 0, len(s.Rows))
		for j, r := range s.Rows {
			title := strings.TrimSpace(r.Title)
			if title == "" {
				return nil, fmt.Errorf("section %d row %d: title is required", i+1, j+1)
			}
			id := strings.TrimSpace(r.ID)
			if id == "" {
				id = title
			}
			if ids[id] {
				return nil, fmt.Errorf("section %d row %d: duplicate id %q", i+1, j+1, id)
			}
			ids[id] = true
			rows = append(rows, &waE2E.ListMessage_Row{
				RowID:       proto.String(id),
				Title:       proto.String(title),
				Description: strPtrOrNil(strings.TrimSpace(r.Description)),
			})
		}
		sections = append(sections, &waE2E.ListMessage_Section{
			Title: strPtrOrNil(strings.TrimSpace(s.Title)),
			Rows:  rows,
		})
	}
	return &waE2E.Message{ListMessage: &waE2E.ListMessage{
		Title:       strPtrOrNil(strings.TrimSpace(spec.Title)),
		Description: proto.String(text),
		ButtonText:  proto.String(buttonText),
		ListType:    waE2E.ListMessage_SINGLE_SELECT.Enum(),
		Sections:    sections,
		FooterText:  strPtrOrNil(strings.TrimSpace(spec.Footer)),
	}}, nil
}

// buttonsNodes returns the extra stanza nodes a NativeFlow buttons message
// needs. The <bot biz_bot="1"/> node goes only to 1:1 chats.
func buttonsNodes(to types.JID, flavor string, now time.Time) []waBinary.Node {
	interactive := waBinary.Node{
		Tag:   "interactive",
		Attrs: waBinary.Attrs{"type": "native_flow", "v": "1"},
		Content: []waBinary.Node{{
			Tag:   "native_flow",
			Attrs: waBinary.Attrs{"v": "9", "name": "mixed"},
		}},
	}
	children := []waBinary.Node{interactive}
	biz := waBinary.Node{Tag: "biz"}
	if flavor == FlavorFull {
		biz.Attrs = waBinary.Attrs{
			"actual_actors":   "2",
			"host_storage":    "2",
			"privacy_mode_ts": strconv.FormatInt(now.Unix(), 10),
		}
		children = append(children, waBinary.Node{
			Tag:     "quality_control",
			Attrs:   waBinary.Attrs{"source_type": "third_party"},
			Content: []waBinary.Node{{Tag: "decision_source", Attrs: waBinary.Attrs{"value": "df"}}},
		})
	}
	biz.Content = children
	nodes := []waBinary.Node{biz}
	if isOneToOne(to) {
		nodes = append(nodes, waBinary.Node{Tag: "bot", Attrs: waBinary.Attrs{"biz_bot": "1"}})
	}
	return nodes
}

func isOneToOne(j types.JID) bool {
	return j.Server == types.DefaultUserServer || j.Server == types.HiddenUserServer
}

// buttonsFallbackText renders a buttons message as plain text, for when the
// interactive send is refused.
func buttonsFallbackText(spec ButtonsSpec) string {
	var b strings.Builder
	if t := strings.TrimSpace(spec.Title); t != "" {
		b.WriteString("*" + t + "*\n")
	}
	b.WriteString(strings.TrimSpace(spec.Text))
	n := 0
	for _, btn := range spec.Buttons {
		label := strings.TrimSpace(btn.Text)
		switch strings.ToLower(strings.TrimSpace(btn.Type)) {
		case ButtonURL:
			b.WriteString("\n🔗 " + label + ": " + strings.TrimSpace(btn.URL))
		case ButtonCall:
			b.WriteString("\n📞 " + label + ": " + strings.TrimSpace(btn.Phone))
		case ButtonCopy:
			b.WriteString("\n📋 " + label + ": " + btn.Code)
		default:
			n++
			b.WriteString(fmt.Sprintf("\n%d. %s", n, label))
		}
	}
	if f := strings.TrimSpace(spec.Footer); f != "" {
		b.WriteString("\n\n_" + f + "_")
	}
	return b.String()
}

// listFallbackText renders a list message as plain text, numbering every row.
func listFallbackText(spec ListSpec) string {
	var b strings.Builder
	if t := strings.TrimSpace(spec.Title); t != "" {
		b.WriteString("*" + t + "*\n")
	}
	b.WriteString(strings.TrimSpace(spec.Text))
	n := 0
	for _, s := range spec.Sections {
		if t := strings.TrimSpace(s.Title); t != "" {
			b.WriteString("\n\n*" + t + "*")
		}
		for _, r := range s.Rows {
			n++
			b.WriteString(fmt.Sprintf("\n%d. %s", n, strings.TrimSpace(r.Title)))
			if d := strings.TrimSpace(r.Description); d != "" {
				b.WriteString(" — " + d)
			}
		}
	}
	if f := strings.TrimSpace(spec.Footer); f != "" {
		b.WriteString("\n\n_" + f + "_")
	}
	return b.String()
}

// Reply describes a tap on a button or list row that came back to us.
type Reply struct {
	Kind string // button | list
	ID   string // button id / row id
	Text string // label the user saw
}

// interactiveReply extracts a button/list tap from an incoming message.
func interactiveReply(m *waE2E.Message) *Reply {
	if m == nil {
		return nil
	}
	if ir := m.GetInteractiveResponseMessage(); ir != nil {
		r := &Reply{Kind: "button", Text: ir.GetBody().GetText()}
		if nf := ir.GetNativeFlowResponseMessage(); nf != nil {
			var p map[string]any
			if json.Unmarshal([]byte(nf.GetParamsJSON()), &p) == nil {
				if id, ok := p["id"].(string); ok {
					r.ID = id
				}
				if r.Text == "" {
					if t, ok := p["display_text"].(string); ok {
						r.Text = t
					}
				}
			}
		}
		return r
	}
	if lr := m.GetListResponseMessage(); lr != nil {
		return &Reply{Kind: "list", ID: lr.GetSingleSelectReply().GetSelectedRowID(), Text: lr.GetTitle()}
	}
	if br := m.GetButtonsResponseMessage(); br != nil {
		return &Reply{Kind: "button", ID: br.GetSelectedButtonID(), Text: br.GetSelectedDisplayText()}
	}
	if tr := m.GetTemplateButtonReplyMessage(); tr != nil {
		return &Reply{Kind: "button", ID: tr.GetSelectedID(), Text: tr.GetSelectedDisplayText()}
	}
	return nil
}
