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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AriOliv/whatsapp-mcp/internal/config"
	"github.com/AriOliv/whatsapp-mcp/internal/mcpserver"
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

	n, err := mgr.LoadAndConnect(ctx)
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "connected %d WhatsApp account(s)\n", n)

	srv := mcpserver.Build(mgr)

	switch cfg.Mode {
	case config.ModeHTTP:
		mux := http.NewServeMux()
		mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("content-type", "application/json")
			fmt.Fprintf(w, `{"ok":true,"accounts":%d}`, n)
		})
		// TODO(phase 4): OAuth 2.1 (authorize/token/register) + QR login page + bearer guard on /mcp.
		httpSrv := &http.Server{Addr: ":" + cfg.Port, Handler: mux}
		go func() { <-ctx.Done(); _ = httpSrv.Close() }()
		fmt.Fprintf(os.Stderr, "HTTP MCP on :%s/mcp\n", cfg.Port)
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
