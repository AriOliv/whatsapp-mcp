package wa

import (
	"encoding/json"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestBuildFlowMessage(t *testing.T) {
	msg, err := buildFlowMessage(FlowSpec{Text: "Preencha", CTA: "Abrir formulário", FlowID: "123",
		FlowToken: "tok", Screen: "WELCOME", Data: map[string]any{"name": "Ari"}, DummyReply: true})
	if err != nil {
		t.Fatal(err)
	}
	nf := msg.GetInteractiveMessage().GetNativeFlowMessage()
	if nf.GetMessageVersion() != 3 {
		t.Fatalf("message version = %d, want 3 (flows don't open with 1)", nf.GetMessageVersion())
	}
	if nf.MessageParamsJSON != nil {
		t.Fatal("messageParamsJSON must stay unset")
	}
	bs := nf.GetButtons()
	if len(bs) != 2 || bs[0].GetName() != "quick_reply" || bs[1].GetName() != "galaxy_message" {
		t.Fatalf("buttons = %v", bs)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(bs[1].GetButtonParamsJSON()), &p); err != nil {
		t.Fatal(err)
	}
	if p["flow_message_version"] != "3" || p["flow_id"] != "123" || p["flow_token"] != "tok" ||
		p["flow_cta"] != "Abrir formulário" || p["flow_action"] != "navigate" || p["mode"] != "published" {
		t.Fatalf("params = %v", p)
	}
	payload := p["flow_action_payload"].(map[string]any)
	if payload["screen"] != "WELCOME" || payload["data"].(map[string]any)["name"] != "Ari" {
		t.Fatalf("payload = %v", payload)
	}
}

func TestBuildFlowMessageErrors(t *testing.T) {
	for name, spec := range map[string]FlowSpec{
		"no text":    {FlowID: "1"},
		"no flow id": {Text: "x"},
	} {
		if _, err := buildFlowMessage(spec); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("%s: want ErrInvalidSpec, got %v", name, err)
		}
	}
	if _, err := buildFlowMessage(FlowSpec{Text: "x", FlowJSON: `{"version":"6.0"}`}); err != nil {
		t.Errorf("inline flow json without id should be accepted: %v", err)
	}
}

func TestFlowReply(t *testing.T) {
	resp := `{"screens":[{"id":"S1","components":[{"name":"n1","type":"TextInput","label":"Nome","value":"Ari"},` +
		`{"name":"n2","type":"Dropdown","label":"Plano","value":"pro"}]}],"version":2}`
	params, _ := json.Marshal(map[string]any{
		"flow_token": "tok-1", "flow_message_version": 1,
		"wa_flow_response_params": map[string]any{"response_message": resp, "flow_id": "123"},
	})
	msg := &waE2E.Message{InteractiveResponseMessage: &waE2E.InteractiveResponseMessage{
		Body: &waE2E.InteractiveResponseMessage_Body{Text: proto.String("Enviado")},
		InteractiveResponseMessage: &waE2E.InteractiveResponseMessage_NativeFlowResponseMessage_{
			NativeFlowResponseMessage: &waE2E.InteractiveResponseMessage_NativeFlowResponseMessage{
				Name: proto.String("galaxy_message"), ParamsJSON: proto.String(string(params)),
			}},
	}}
	r := interactiveReply(msg)
	if r == nil || r.Kind != "flow" || r.ID != "tok-1" || r.Form["Nome"] != "Ari" || r.Form["Plano"] != "pro" {
		t.Fatalf("reply = %+v", r)
	}
	if got := messageText(msg); got != "[flow:tok-1] Nome: Ari; Plano: pro" {
		t.Fatalf("body = %q", got)
	}

	// data-endpoint flows send a flat object
	_, form := flowAnswers(`{"flow_token":"t","email":"a@b.c"}`)
	if form["email"] != "a@b.c" || len(form) != 1 {
		t.Fatalf("flat form = %v", form)
	}
}
