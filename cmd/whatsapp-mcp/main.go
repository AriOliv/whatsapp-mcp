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
	"github.com/AriOliv/whatsapp-mcp/internal/mcpserver"
	"github.com/AriOliv/whatsapp-mcp/internal/oauth"
	"github.com/AriOliv/whatsapp-mcp/internal/store"
	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, db, err := store.Open(cfg.DBURL, cfg.IsPostgres())
	if err != nil {
		fatal(err)
	}
	if err := st.Init(ctx); err != nil {
		fatal(err)
	}

	mgr, err := wa.New(ctx, db, cfg.IsPostgres(), st, cfg.DeviceName)
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
		id, serr := mgr.SendText(ctx, "", own, "✅ Smoke test: whatsmeow MCP pareado — enviando e recebendo (Avenia).")
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

	n, err := mgr.LoadAndConnect(ctx)
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "connected %d WhatsApp account(s)\n", n)

	srv := mcpserver.Build(mgr)

	switch cfg.Mode {
	case config.ModeHTTP:
		if len(cfg.JWTSecret) < 32 {
			fatal(fmt.Errorf("MCP_JWT_SECRET must be set (>=32 chars) for HTTP mode"))
		}
		mux := http.NewServeMux()
		oauthStore := oauth.NewStore(cfg.JWTSecret, cfg.PublicURL, cfg.PublicURL+"/mcp", db, mgr.HasDevice)
		if err := oauthStore.Init(ctx); err != nil {
			fatal(err)
		}
		handlers := oauth.NewHandlers(oauthStore, mgr, cfg.PublicURL)
		mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
		handlers.Register(mux, mcpHandler) // mounts bearer-guarded /mcp + OAuth + login routes
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("content-type", "application/json")
			fmt.Fprintf(w, `{"ok":true,"accounts":%d}`, n)
		})
		httpSrv := &http.Server{Addr: ":" + cfg.Port, Handler: mux}
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

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fatal:", err)
	os.Exit(1)
}
