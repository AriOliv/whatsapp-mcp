package wa

import (
	"errors"
	"testing"
)

func testSpec() FormSpec {
	return FormSpec{Title: "Cadastro", Fields: []FormField{
		{ID: "nome", Label: "Nome", Required: true},
		{ID: "cpf", Label: "CPF", Type: "cpf", Required: true},
		{ID: "email", Label: "E-mail", Type: "email"},
		{ID: "plano", Label: "Plano", Type: "select", Options: []string{"basico", "pro"}, Required: true},
		{ID: "canais", Label: "Canais", Type: "checkbox", Options: []string{"whatsapp", "email"}},
		{ID: "aceite", Label: "Aceito os termos", Type: "checkbox", Required: true},
		{ID: "nasc", Label: "Nascimento", Type: "date"},
	}}
}

func TestValidateFormSpec(t *testing.T) {
	s := testSpec()
	if err := validateFormSpec(&s); err != nil {
		t.Fatal(err)
	}
	if s.Fields[0].Type != FieldText {
		t.Fatalf("empty type should default to text, got %q", s.Fields[0].Type)
	}
	bad := map[string]FormSpec{
		"no title":       {Fields: []FormField{{ID: "a", Label: "A"}}},
		"no fields":      {Title: "x"},
		"bad id":         {Title: "x", Fields: []FormField{{ID: "1a", Label: "A"}}},
		"dup id":         {Title: "x", Fields: []FormField{{ID: "a", Label: "A"}, {ID: "a", Label: "B"}}},
		"no label":       {Title: "x", Fields: []FormField{{ID: "a"}}},
		"select no opts": {Title: "x", Fields: []FormField{{ID: "a", Label: "A", Type: "select"}}},
		"unknown type":   {Title: "x", Fields: []FormField{{ID: "a", Label: "A", Type: "file"}}},
		"dup option":     {Title: "x", Fields: []FormField{{ID: "a", Label: "A", Type: "radio", Options: []string{"x", "x"}}}},
		"ttl too long":   {Title: "x", ExpiresIn: 31 * 24 * 60, Fields: []FormField{{ID: "a", Label: "A"}}},
	}
	for name, spec := range bad {
		if err := validateFormSpec(&spec); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("%s: want ErrInvalidSpec, got %v", name, err)
		}
	}
}

func TestValidateFormAnswers(t *testing.T) {
	s := testSpec()
	_ = validateFormSpec(&s)
	ok, errs := validateFormAnswers(s, map[string][]string{
		"nome": {"  Ari  "}, "cpf": {"529.982.247-25"}, "email": {"ari@avenia.io"}, "plano": {"pro"},
		"canais": {"whatsapp", "email"}, "aceite": {"on"}, "nasc": {"1990-05-01"},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	want := map[string]string{"nome": "Ari", "cpf": "52998224725", "email": "ari@avenia.io", "plano": "pro",
		"canais": "whatsapp, email", "aceite": "sim", "nasc": "1990-05-01"}
	for k, v := range want {
		if ok[k] != v {
			t.Errorf("%s = %q, want %q", k, ok[k], v)
		}
	}

	_, errs = validateFormAnswers(s, map[string][]string{
		"cpf": {"111.111.111-11"}, "email": {"nao-e-email"}, "plano": {"enterprise"}, "canais": {"sms"}, "nasc": {"01/05/1990"},
	})
	for _, id := range []string{"nome", "cpf", "email", "plano", "canais", "aceite", "nasc"} {
		if errs[id] == "" {
			t.Errorf("%s: expected an error", id)
		}
	}
}

func TestCPFCNPJ(t *testing.T) {
	for d, want := range map[string]bool{"52998224725": true, "52998224724": false, "00000000000": false, "123": false} {
		if validCPF(d) != want {
			t.Errorf("cpf %s = %v", d, !want)
		}
	}
	for d, want := range map[string]bool{"11222333000181": true, "11222333000180": false, "11111111111111": false} {
		if validCNPJ(d) != want {
			t.Errorf("cnpj %s = %v", d, !want)
		}
	}
}
