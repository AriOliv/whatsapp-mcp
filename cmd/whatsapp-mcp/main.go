// Command whatsapp-mcp is a single-binary WhatsApp MCP server built on whatsmeow.
//
//	whatsapp-mcp pair    # interactive QR pairing (local), then exit
//	whatsapp-mcp         # run the MCP server (MCP_MODE=stdio|http)
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AriOliv/whatsapp-mcp/internal/config"
	"github.com/AriOliv/whatsapp-mcp/internal/gateway"
	"github.com/AriOliv/whatsapp-mcp/internal/mcpserver"
	"github.com/AriOliv/whatsapp-mcp/internal/oauth"
	"github.com/AriOliv/whatsapp-mcp/internal/safehttp"
	"github.com/AriOliv/whatsapp-mcp/internal/store"
	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	gatewayMode := cfg.Mode == config.ModeHTTP && cfg.AuthMode == config.AuthGateway
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, db, err := store.Open(cfg.DBURL, cfg.IsPostgres())
	if err != nil {
		fatal(err)
	}
	if err := st.Init(ctx); err != nil {
		fatal(err)
	}

	settings := settingsFor(cfg)
	var waOpts []wa.Option
	if settings.strictAccounts {
		waOpts = append(waOpts, wa.WithStrictAccounts())
	}
	if settings.historyOff {
		waOpts = append(waOpts, wa.WithHistorySyncDefaultOff())
	}
	if settings.safeMedia {
		fetcher := safehttp.New(safehttp.Options{AllowedHosts: gateway.ParseDomains(cfg.MediaAllowedHosts)})
		waOpts = append(waOpts, wa.WithMediaFetcher(fetcher.Fetch))
	}
	// Refresh tokens go with a purged account. The store is created after the
	// manager (it needs HasDevice), hence the late-bound pointer.
	var oauthStore *oauth.Store
	waOpts = append(waOpts, wa.WithPurgeHook(func(ctx context.Context, account string) error {
		if oauthStore == nil {
			return nil
		}
		return oauthStore.RevokeSub(ctx, account)
	}))
	mgr, err := wa.New(ctx, db, cfg.IsPostgres(), st, cfg.DeviceName, waOpts...)
	if err != nil {
		fatal(err)
	}

	// `whatsapp-mcp pair` → interactive QR pairing, then exit.
	if len(os.Args) > 1 && os.Args[1] == "pair" {
		if err := mgr.PairInteractive(ctx); err != nil {
			fatal(err)
		}
		return
	}

	// `whatsapp-mcp pair-code <number>` → phone-code pairing + a self send/receive
	// smoke test, then exit.
	if len(os.Args) > 2 && os.Args[1] == "pair-code" {
		if err := mgr.PairWithCode(ctx, os.Args[2], func(code string) {
			fmt.Printf("PAIRING_CODE=%s\n", code)
			fmt.Fprintf(os.Stderr, "\n=== WhatsApp → Linked devices → Link with phone number → enter: %s ===\n", code)
		}); err != nil {
			fatal(err)
		}
		if !mgr.WaitReady(ctx, 30*time.Second) {
			fmt.Fprintln(os.Stderr, "warning: client not fully ready after 30s")
		}
		own := mgr.DefaultNumber()
		id, serr := mgr.SendText(ctx, "", own, "✅ Smoke test: whatsmeow MCP pareado — enviando e recebendo.")
		if serr != nil {
			fmt.Printf("SEND_ERROR=%v\n", serr)
		} else {
			fmt.Printf("SENT id=%s to=%s\n", id, own)
		}
		time.Sleep(4 * time.Second) // let the echo arrive and be stored
		chats, _ := mgr.ListChats(ctx, "", 10)
		fmt.Printf("STORED_CHATS=%d\n", len(chats))
		return
	}

	gwAccounts := wa.ParseAccounts(cfg.InboundWebhookAccounts)
	if err := mgr.SetWebhook(ctx, wa.WebhookConfig{URL: cfg.InboundWebhookURL, Secret: cfg.InboundWebhookSecret,
		Accounts: gwAccounts}); err != nil {
		fatal(err)
	}

	n, err := mgr.LoadAndConnect(ctx)
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "connected %d WhatsApp account(s)\n", n)

	if gatewayMode {
		if err := serveGateway(ctx, cfg, st, mgr); err != nil {
			fatal(err)
		}
		return
	}

	srv := mcpserver.Build(mgr)

	switch cfg.Mode {
	case config.ModeHTTP:
		if len(cfg.JWTSecret) < 32 {
			fatal(fmt.Errorf("MCP_JWT_SECRET (or MCP_JWT_SECRET_FILE) must hold >=32 chars for HTTP mode"))
		}
		if settings.sendLimit {
			mcpserver.LimitSends(srv, mcpserver.NewSendLimiter(cfg.SendRatePerMinute, cfg.SendRateBurst))
		}
		oauthStore = oauth.NewStore(cfg.JWTSecret, cfg.PublicURL, cfg.PublicURL+"/mcp", db, mgr.HasDevice)
		if err := oauthStore.Init(ctx); err != nil {
			fatal(err)
		}
		for _, init := range []func(context.Context) error{st.InitForms, st.InitOutbox} {
			if err := init(ctx); err != nil {
				fatal(err)
			}
		}
		if cfg.FormsEnabled {
			if err := mgr.InitForms(ctx, cfg.PublicURL); err != nil {
				fatal(err)
			}
		}
		mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
		httpSrv := &http.Server{
			Addr:              ":" + cfg.Port,
			Handler:           oauthMux(cfg, oauthStore, mgr, mcpHandler, gwAccounts),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() { <-ctx.Done(); _ = httpSrv.Close() }()
		fmt.Fprintf(os.Stderr, "HTTP MCP (OAuth) on %s/mcp\n", cfg.PublicURL)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fatal(err)
		}
	default:
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
			fatal(err)
		}
	}
}

// serveGateway runs gateway mode: the only routes are /mcp behind the
// gateway's JWT and a /healthz that lists nothing.
func serveGateway(ctx context.Context, cfg config.Config, st *store.Store, mgr *wa.Manager) error {
	if cfg.GatewayJWKSURL == "" {
		return fmt.Errorf("GATEWAY_JWKS_URL is required in gateway mode")
	}
	verifier, err := gateway.NewVerifier(gateway.NewJWKS(cfg.GatewayJWKSURL), cfg.GatewayJWTIssuer,
		cfg.GatewayJWTAudience, gateway.ParseDomains(cfg.AllowedEmailDomains))
	if err != nil {
		return fmt.Errorf("gateway mode: %w (set GATEWAY_JWT_ISSUER, GATEWAY_JWT_AUDIENCE, ALLOWED_EMAIL_DOMAINS)", err)
	}
	for _, init := range []func(context.Context) error{st.InitOwners, st.InitForms, st.InitOutbox} {
		if err := init(ctx); err != nil {
			return err
		}
	}
	srv := mcpserver.BuildGateway(mgr, mcpserver.Gateway{
		Accounts: st,
		Pairing:  gateway.NewPairing(st, mgr),
		Limiter:  mcpserver.NewSendLimiter(cfg.SendRatePerMinute, cfg.SendRateBurst),
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           gateway.NewMux(verifier, mcpHandler),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { <-ctx.Done(); _ = httpSrv.Close() }()
	fmt.Fprintf(os.Stderr, "HTTP MCP (gateway auth) on :%s/mcp\n", cfg.Port)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fatal:", err)
	os.Exit(1)
}
