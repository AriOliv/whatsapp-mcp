package wa

// Web forms. WhatsApp Flows (native forms) only open for flows published on a
// WhatsApp Business API account, so for a personal account a form is a page:
// the message carries a cta_url button to PUBLIC_URL/f/<token>, the recipient
// fills it in the in-app browser, and the answers come back out of band (store +
// webhook) — the chat only gets a short receipt.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

// Form field types.
const (
	FieldText     = "text"
	FieldTextarea = "textarea"
	FieldEmail    = "email"
	FieldPhone    = "phone"
	FieldNumber   = "number"
	FieldDate     = "date"
	FieldCPF      = "cpf"
	FieldCNPJ     = "cnpj"
	FieldSelect   = "select"
	FieldRadio    = "radio"
	FieldCheckbox = "checkbox"
)

const (
	maxFormFields    = 30
	maxFieldLen      = 2000
	maxTextareaLen   = 8000
	defaultFormTTL   = 24 * time.Hour
	maxFormTTL       = 30 * 24 * time.Hour
	formRetention    = 30 * 24 * time.Hour
	formReceiptText  = "✅ Recebemos suas respostas. Obrigado!"
	defaultFormCTA   = "Preencher"
	defaultFormIntro = "Toque no botão para preencher o formulário."
)

// ErrFormNotFound is returned for an unknown, foreign or malformed form/token.
var ErrFormNotFound = errors.New("form not found")

// ErrFormClosed is returned when a form was already submitted or has expired.
var ErrFormClosed = appstore.ErrFormClosed

// FormField is one input of a form.
type FormField struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // text|textarea|email|phone|number|date|cpf|cnpj|select|radio|checkbox
	Required    bool     `json:"required,omitempty"`
	Options     []string `json:"options,omitempty"` // select/radio/checkbox choices (checkbox without options = a single yes/no)
	Placeholder string   `json:"placeholder,omitempty"`
	Help        string   `json:"help,omitempty"`
}

// FormSpec describes a form and the message that carries its link.
type FormSpec struct {
	Title       string      `json:"title"`                 // page title (and message header)
	Description string      `json:"description,omitempty"` // shown on the page above the fields
	Text        string      `json:"text,omitempty"`        // message body
	Button      string      `json:"button,omitempty"`      // button label (default Preencher)
	Submit      string      `json:"submit,omitempty"`      // page submit label (default Enviar)
	Fields      []FormField `json:"fields"`
	ExpiresIn   int         `json:"expiresInMinutes,omitempty"` // default 24h, max 30 days
	Receipt     *bool       `json:"receipt,omitempty"`          // send the "received" message in chat (default true)
}

// FormSent is what SendForm returns.
type FormSent struct {
	ID        string    `json:"form_id"`
	Link      string    `json:"link"`
	ExpiresAt time.Time `json:"expires_at"`
	SendResult
}

// FormView is a form as returned to its owner.
type FormView struct {
	ID          string            `json:"form_id"`
	To          string            `json:"to"`
	Title       string            `json:"title"`
	Status      string            `json:"status"` // open | submitted | expired
	CreatedAt   time.Time         `json:"created_at"`
	ExpiresAt   time.Time         `json:"expires_at"`
	SubmittedAt *time.Time        `json:"submitted_at,omitempty"`
	Answers     map[string]string `json:"answers,omitempty"`
}

var fieldIDRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,39}$`)

// validateFormSpec checks a spec and fills its defaults.
func validateFormSpec(spec *FormSpec) error {
	spec.Title = strings.TrimSpace(spec.Title)
	if spec.Title == "" {
		return invalid("title is required")
	}
	if len(spec.Fields) == 0 || len(spec.Fields) > maxFormFields {
		return invalid("need between 1 and %d fields, got %d", maxFormFields, len(spec.Fields))
	}
	if spec.ExpiresIn < 0 || time.Duration(spec.ExpiresIn)*time.Minute > maxFormTTL {
		return invalid("expiresInMinutes must be between 1 and %d", int(maxFormTTL/time.Minute))
	}
	ids := map[string]bool{}
	for i := range spec.Fields {
		f := &spec.Fields[i]
		f.ID, f.Label = strings.TrimSpace(f.ID), strings.TrimSpace(f.Label)
		f.Type = strings.ToLower(strings.TrimSpace(f.Type))
		if f.Type == "" {
			f.Type = FieldText
		}
		if !fieldIDRe.MatchString(f.ID) {
			return invalid("field %d: id must be a letter followed by letters, digits or _ (max 40)", i+1)
		}
		if ids[f.ID] {
			return invalid("field %d: duplicate id %q", i+1, f.ID)
		}
		ids[f.ID] = true
		if f.Label == "" {
			return invalid("field %d: label is required", i+1)
		}
		switch f.Type {
		case FieldText, FieldTextarea, FieldEmail, FieldPhone, FieldNumber, FieldDate, FieldCPF, FieldCNPJ:
		case FieldSelect, FieldRadio:
			if len(f.Options) == 0 {
				return invalid("field %d: %s needs options", i+1, f.Type)
			}
		case FieldCheckbox:
		default:
			return invalid("field %d: unknown type %q", i+1, f.Type)
		}
		seen := map[string]bool{}
		for j, o := range f.Options {
			o = strings.TrimSpace(o)
			if o == "" || seen[o] {
				return invalid("field %d: option %d is empty or repeated", i+1, j+1)
			}
			seen[o] = true
			f.Options[j] = o
		}
	}
	return nil
}

// validateFormAnswers checks submitted values against the spec. raw holds every
// value posted for a field (checkboxes may post several). It returns the clean
// answers and, for invalid input, a message per field id.
func validateFormAnswers(spec FormSpec, raw map[string][]string) (map[string]string, map[string]string) {
	out, errs := map[string]string{}, map[string]string{}
	for _, f := range spec.Fields {
		vals := raw[f.ID]
		v := ""
		if len(vals) > 0 {
			v = strings.TrimSpace(vals[0])
		}
		if f.Type == FieldCheckbox && len(f.Options) > 0 {
			var picked []string
			allowed := map[string]bool{}
			for _, o := range f.Options {
				allowed[o] = true
			}
			for _, x := range vals {
				if allowed[x] {
					picked = append(picked, x)
				} else if x != "" {
					errs[f.ID] = "Opção inválida."
				}
			}
			v = strings.Join(picked, ", ")
		}
		if v == "" {
			if f.Required {
				errs[f.ID] = "Campo obrigatório."
			}
			continue
		}
		limit := maxFieldLen
		if f.Type == FieldTextarea {
			limit = maxTextareaLen
		}
		if len(v) > limit {
			errs[f.ID] = fmt.Sprintf("Máximo de %d caracteres.", limit)
			continue
		}
		switch f.Type {
		case FieldEmail:
			if a, err := mail.ParseAddress(v); err != nil || a.Address != v || !strings.Contains(v, ".") {
				errs[f.ID] = "E-mail inválido."
			}
		case FieldPhone:
			if d := onlyDigits(v); len(d) < 10 || len(d) > 15 {
				errs[f.ID] = "Telefone inválido."
			}
		case FieldNumber:
			if _, err := strconv.ParseFloat(strings.Replace(v, ",", ".", 1), 64); err != nil {
				errs[f.ID] = "Número inválido."
			}
		case FieldDate:
			if _, err := time.Parse("2006-01-02", v); err != nil {
				errs[f.ID] = "Data inválida."
			}
		case FieldCPF:
			if d := onlyDigits(v); !validCPF(d) {
				errs[f.ID] = "CPF inválido."
			} else {
				v = d
			}
		case FieldCNPJ:
			if d := onlyDigits(v); !validCNPJ(d) {
				errs[f.ID] = "CNPJ inválido."
			} else {
				v = d
			}
		case FieldSelect, FieldRadio:
			ok := false
			for _, o := range f.Options {
				ok = ok || o == v
			}
			if !ok {
				errs[f.ID] = "Opção inválida."
			}
		case FieldCheckbox:
			if len(f.Options) == 0 {
				v = "sim"
			}
		}
		if _, bad := errs[f.ID]; !bad {
			out[f.ID] = v
		}
	}
	return out, errs
}

func validCPF(d string) bool {
	if len(d) != 11 || strings.Count(d, d[:1]) == 11 {
		return false
	}
	return checkDigits(d, []int{10, 9, 8, 7, 6, 5, 4, 3, 2}, 9) && checkDigits(d, []int{11, 10, 9, 8, 7, 6, 5, 4, 3, 2}, 10)
}

func validCNPJ(d string) bool {
	if len(d) != 14 || strings.Count(d, d[:1]) == 14 {
		return false
	}
	return checkDigits(d, []int{5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}, 12) &&
		checkDigits(d, []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}, 13)
}

// checkDigits verifies the mod-11 check digit at position pos.
func checkDigits(d string, weights []int, pos int) bool {
	sum := 0
	for i, w := range weights {
		sum += int(d[i]-'0') * w
	}
	r := sum % 11
	want := 0
	if r >= 2 {
		want = 11 - r
	}
	return int(d[pos]-'0') == want
}

func randToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// FormTokenHash is how a link token is stored and looked up.
func FormTokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// SendForm stores a form and sends its link as a cta_url button. If the button
// is refused, the link goes out as plain text.
func (m *Manager) SendForm(ctx context.Context, account, to string, spec FormSpec) (FormSent, error) {
	publicURL := m.publicURL
	if err := validateFormSpec(&spec); err != nil {
		return FormSent{}, err
	}
	if !strings.HasPrefix(publicURL, "https://") {
		return FormSent{}, errors.New("forms are disabled: they need the HTTP server with an https PUBLIC_URL")
	}
	cli, err := m.clientFor(account)
	if err != nil {
		return FormSent{}, err
	}
	if account == "" {
		account = accountKey(cli.Store.ID)
	}
	jid, err := resolveJID(to)
	if err != nil {
		return FormSent{}, err
	}
	ttl := defaultFormTTL
	if spec.ExpiresIn > 0 {
		ttl = time.Duration(spec.ExpiresIn) * time.Minute
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return FormSent{}, err
	}
	now := time.Now()
	token := randToken(32)
	f := appstore.Form{ID: "frm_" + randToken(9), TokenHash: FormTokenHash(token), Account: account, To: jid.String(),
		Spec: raw, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	if err := m.store.CreateForm(ctx, f); err != nil {
		return FormSent{}, err
	}
	link := strings.TrimRight(publicURL, "/") + "/f/" + token
	text := strings.TrimSpace(spec.Text)
	if text == "" {
		text = defaultFormIntro
	}
	label := strings.TrimSpace(spec.Button)
	if label == "" {
		label = defaultFormCTA
	}
	res, err := m.SendButtons(ctx, account, to, ButtonsSpec{Text: text, Title: spec.Title,
		Buttons: []Button{{Type: ButtonURL, Text: label, URL: link}}}, "", true)
	if err != nil {
		return FormSent{}, err
	}
	return FormSent{ID: f.ID, Link: link, ExpiresAt: f.ExpiresAt, SendResult: res}, nil
}

// OpenForm returns the form behind a link token, for the page.
func (m *Manager) OpenForm(ctx context.Context, token string) (*appstore.Form, FormSpec, error) {
	var spec FormSpec
	if token == "" || len(token) > 100 {
		return nil, spec, ErrFormNotFound
	}
	f, err := m.store.FormByToken(ctx, FormTokenHash(token))
	if err != nil {
		return nil, spec, err
	}
	if f == nil {
		return nil, spec, ErrFormNotFound
	}
	if err := json.Unmarshal(f.Spec, &spec); err != nil {
		return nil, spec, err
	}
	return f, spec, nil
}

// SubmitForm validates and records the answers posted through a link. On
// success it pushes a form_submit event to the webhook and (unless disabled)
// sends a short receipt in the chat — never the answers themselves.
func (m *Manager) SubmitForm(ctx context.Context, token string, raw map[string][]string) (map[string]string, error) {
	f, spec, err := m.OpenForm(ctx, token)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if f.Status != appstore.FormOpen || f.Expired(now) {
		return nil, ErrFormClosed
	}
	answers, errs := validateFormAnswers(spec, raw)
	if len(errs) > 0 {
		return errs, invalid("some answers are invalid")
	}
	body, err := json.Marshal(answers)
	if err != nil {
		return nil, err
	}
	if err := m.store.SubmitForm(ctx, f.ID, body, now); err != nil {
		return nil, err
	}
	if m.webhook != nil && m.webhook.Accounts[f.Account] {
		m.pushInbound(&InboundEvent{Account: f.Account, ID: f.ID, From: jidUser(f.To), FromJID: f.To,
			TS: now.UnixMilli(), Type: "form_submit", Text: spec.Title, ReplyID: f.ID, Form: answers})
	}
	if spec.Receipt == nil || *spec.Receipt {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := m.SendText(ctx, f.Account, f.To, formReceiptText); err != nil {
				m.log.Warnf("form %s: receipt not sent: %v", f.ID, err)
			}
		}()
	}
	return nil, nil
}

// GetForm returns a form of account (status and answers).
func (m *Manager) GetForm(ctx context.Context, account, id string) (*FormView, error) {
	if account == "" {
		cli, err := m.clientFor("")
		if err != nil {
			return nil, err
		}
		account = accountKey(cli.Store.ID)
	}
	f, err := m.store.GetForm(ctx, account, id)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrFormNotFound
	}
	var spec FormSpec
	_ = json.Unmarshal(f.Spec, &spec)
	v := &FormView{ID: f.ID, To: jidUser(f.To), Title: spec.Title, Status: f.Status, CreatedAt: f.CreatedAt, ExpiresAt: f.ExpiresAt}
	if f.Expired(time.Now()) {
		v.Status = "expired"
	}
	if f.Status == appstore.FormSubmitted {
		t := f.SubmittedAt
		v.SubmittedAt = &t
		_ = json.Unmarshal(f.Answers, &v.Answers)
	}
	return v, nil
}

// InitForms prepares the forms table and its retention loop and enables forms;
// links point at publicURL/f/<token>.
func (m *Manager) InitForms(ctx context.Context, publicURL string) error {
	if err := m.store.InitForms(ctx); err != nil {
		return err
	}
	m.publicURL = strings.TrimRight(publicURL, "/")
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if n, err := m.store.PurgeForms(ctx, formRetention, time.Now()); err == nil && n > 0 {
				m.log.Infof("forms: purged %d old form(s)", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return nil
}

func jidUser(j string) string {
	if i := strings.IndexByte(j, '@'); i >= 0 {
		return j[:i]
	}
	return j
}
