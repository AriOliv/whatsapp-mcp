package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AriOliv/whatsapp-mcp/internal/config"
	"github.com/AriOliv/whatsapp-mcp/internal/oauth"
	"github.com/AriOliv/whatsapp-mcp/internal/store"
	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

func TestSettingsFor(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
		want modeSettings
	}{
		{"stdio is a single local user", config.Config{Mode: config.ModeStdio}, modeSettings{}},
		{"oauth over http", config.Config{Mode: config.ModeHTTP, AuthMode: config.AuthOAuth},
			modeSettings{historyOff: true, safeMedia: true, sendLimit: true}},
		{"gateway over http", config.Config{Mode: config.ModeHTTP, AuthMode: config.AuthGateway},
			modeSettings{strictAccounts: true, historyOff: true, safeMedia: true, sendLimit: true}},
	}
	for _, c := range cases {
		if got := settingsFor(c.cfg); got != c.want {
			t.Errorf("%s: settings = %+v, want %+v", c.name, got, c.want)
		}
	}
}

type fakeHealth []wa.AccountHealth

func (f fakeHealth) Health() ([]wa.AccountHealth, uint64, uint64) { return f, 7, 3 }

func TestHealthzListsNoNumbers(t *testing.T) {
	src := fakeHealth{
		{Account: "5511111111111", State: wa.AccountConnected},
		{Account: "5522222222222", State: wa.AccountNeedsRepair},
	}
	rec := httptest.NewRecorder()
	healthHandler(src)(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	body := rec.Body.String()
	for _, n := range []string{"5511111111111", "5522222222222", "account"} {
		if strings.Contains(body, n) {
			t.Fatalf("healthz leaks %q: %s", n, body)
		}
	}
	for _, want := range []string{`"paired":1`, `"needs_repair":1`, `"connects":7`, `"ok":true`} {
		if !strings.Contains(body, want) {
			t.Fatalf("healthz lacks %s: %s", want, body)
		}
	}
}

func TestOAuthRouteSurface(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dsn := "file:" + filepath.Join(t.TempDir(), "m.db") + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	st, db, err := store.Open(dsn, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Init(ctx); err != nil {
		t.Fatal(err)
	}
	mgr, err := wa.New(ctx, db, false, st, "")
	if err != nil {
		t.Fatal(err)
	}
	oauthStore := oauth.NewStore(strings.Repeat("k", 32), "https://mcp.example.com", "https://mcp.example.com/mcp", db, mgr.HasDevice)
	if err := oauthStore.Init(ctx); err != nil {
		t.Fatal(err)
	}
	mcpOK := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	cases := []struct {
		name     string
		forms    bool
		apiToken string
		method   string
		path     string
		want     int
	}{
		{"forms off by default", false, "", http.MethodGet, "/f/sometoken", http.StatusNotFound},
		{"forms on when enabled", true, "", http.MethodGet, "/f/sometoken", -http.StatusNotFound},
		{"no gateway api without a token", false, "", http.MethodPost, "/api/send", http.StatusNotFound},
		{"gateway api with a token", false, strings.Repeat("t", 32), http.MethodPost, "/api/send", -http.StatusNotFound},
		{"mcp needs a bearer", false, "", http.MethodPost, "/mcp", http.StatusUnauthorized},
		{"healthz", false, "", http.MethodGet, "/healthz", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Config{PublicURL: "https://mcp.example.com", JWTSecret: strings.Repeat("k", 32),
				FormsEnabled: c.forms, GatewayAPIToken: c.apiToken}
			mux := oauthMux(cfg, oauthStore, mgr, mcpOK, map[string]bool{"5511111111111": true})
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader("{}")))
			body, _ := io.ReadAll(rec.Body)
			if c.want < 0 {
				if rec.Code == -c.want {
					t.Fatalf("%s %s = %d, want it mounted", c.method, c.path, rec.Code)
				}
				return
			}
			if rec.Code != c.want {
				t.Fatalf("%s %s = %d, want %d (%s)", c.method, c.path, rec.Code, c.want, body)
			}
		})
	}
}
