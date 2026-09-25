// MCP App wiring: the interactive UI Claude renders for the WhatsApp tools.
//
// One HTML resource backs every card. Tools point at it with
// `_meta.ui.resourceUri`, and the card to render is chosen by the `kind`
// discriminator in each tool's structured content (see view.go). A single
// resource means one permission prompt for the user and one bundle to ship,
// instead of one per tool.
//
// Hosts without MCP Apps support ignore `_meta.ui` entirely and still get the
// text + structured content, so this is purely additive.
package mcpserver

import (
	"context"
	_ "embed"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// appHTML is the built app bundle: one self-contained HTML file (no external
// scripts, styles or fonts — the host's CSP blocks them by default).
//
//go:embed ui/app.html
var appHTML string

const (
	// appURI is the single UI resource every card is rendered from.
	appURI = "ui://whatsapp/app.html"
	// appMIME marks the resource as an MCP App to the host.
	appMIME = "text/html;profile=mcp-app"
)

// appResourceMeta is the resource's `_meta`.
//
//   - prefersBorder:false — the cards draw their own surface with host tokens;
//     a host border around it would double the frame.
//   - csp.resourceDomains — the host serves Anthropic Sans from assets.claude.ai
//     (applyHostFonts injects the @font-face rules), and WhatsApp profile
//     pictures come from pps.whatsapp.net. Everything else stays blocked.
var appResourceMeta = mcp.Meta{
	"ui": map[string]any{
		"prefersBorder": false,
		"csp": map[string]any{
			"resourceDomains": []string{"https://assets.claude.ai", "https://pps.whatsapp.net"},
		},
	},
}

// uiMeta is the per-tool `_meta` that points a tool at the app.
var uiMeta = mcp.Meta{"ui": map[string]any{"resourceUri": appURI}}

// registerApp publishes the UI resource.
func registerApp(s *mcp.Server) {
	s.AddResource(&mcp.Resource{
		URI:         appURI,
		Name:        "WhatsApp",
		Title:       "WhatsApp",
		Description: "Interactive WhatsApp cards: chats, conversations, contacts, groups, media and send confirmations.",
		MIMEType:    appMIME,
		Meta:        appResourceMeta,
	}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      appURI,
				MIMEType: appMIME,
				Text:     appHTML,
				Meta:     appResourceMeta,
			}},
		}, nil
	})
}

// withUI returns a copy of the tool that renders through the app.
func withUI(t *mcp.Tool) *mcp.Tool {
	t.Meta = uiMeta
	return t
}
