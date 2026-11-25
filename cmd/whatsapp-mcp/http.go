package main

import (
	"encoding/json"
	"net/http"

	"github.com/AriOliv/whatsapp-mcp/internal/config"
	"github.com/AriOliv/whatsapp-mcp/internal/formweb"
	"github.com/AriOliv/whatsapp-mcp/internal/httpapi"
	"github.com/AriOliv/whatsapp-mcp/internal/oauth"
	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

// modeSettings are the protections each run mode turns on. Every HTTP mode
// serves several people, so it gets the safe media fetcher, the send budget
// and history sync off by default; only gateway mode needs strict accounts,
// since OAuth always names the caller's own number.
type modeSettings struct {
	strictAccounts bool
	historyOff     bool
	safeMedia      bool
	sendLimit      bool
}

func settingsFor(cfg config.Config) modeSettings {
	if cfg.Mode != config.ModeHTTP {
		return modeSettings{}
	}
	return modeSettings{
		strictAccounts: cfg.AuthMode == config.AuthGateway,
		historyOff:     true,
		safeMedia:      true,
		sendLimit:      true,
	}
}

type healthSource interface {
	Health() ([]wa.AccountHealth, uint64, uint64)
}

// healthHandler reports counts only. Listing accounts would hand every
// unauthenticated caller the phone numbers this server holds.
func healthHandler(src healthSource) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		accounts, connects, disconnects := src.Health()
		live, needRepair := 0, 0
		for _, a := range accounts {
			if a.State == wa.AccountNeedsRepair {
				needRepair++
			} else {
				live++
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":           true,
			"paired":       live,
			"needs_repair": needRepair,
			"connects":     connects,
			"disconnects":  disconnects,
		})
	}
}

// oauthMux is the HTTP surface of OAuth mode: the authorization server, the
// pairing login, /mcp behind the bearer guard, /healthz, and /f/* and /api/*
// only when explicitly enabled.
func oauthMux(cfg config.Config, st *oauth.Store, mgr *wa.Manager, mcpHandler http.Handler, gwAccounts map[string]bool) *http.ServeMux {
	mux := http.NewServeMux()
	oauth.NewHandlers(st, mgr, cfg.PublicURL, oauth.Config{AllowedRedirectHosts: cfg.OAuthRedirectHosts}).
		Register(mux, mcpHandler)
	if cfg.FormsEnabled {
		formweb.New(mgr, []byte(cfg.JWTSecret)).Register(mux)
	}
	if api := httpapi.New(mgr, cfg.GatewayAPIToken, gwAccounts); api != nil {
		api.Register(mux)
	}
	mux.Handle("GET /healthz", healthHandler(mgr))
	return mux
}
