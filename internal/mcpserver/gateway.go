package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AriOliv/whatsapp-mcp/internal/authctx"
	"github.com/AriOliv/whatsapp-mcp/internal/gateway"
	"github.com/AriOliv/whatsapp-mcp/internal/wa"
)

// AccountResolver maps a gateway owner to the number bound to them.
type AccountResolver interface {
	AccountOf(ctx context.Context, owner string) (string, error)
}

// PairingService is the connect / status / disconnect lifecycle.
type PairingService interface {
	Connect(ctx context.Context, owner, phone string) (gateway.ConnectResult, error)
	Status(ctx context.Context, owner string) (gateway.Status, error)
	Disconnect(ctx context.Context, owner string) (gateway.DisconnectResult, error)
}

// Gateway holds what gateway mode adds on top of the normal tool catalog.
type Gateway struct {
	Accounts AccountResolver
	Pairing  PairingService
	Limiter  *SendLimiter
}

// Tools that work without a bound number.
var accountFreeTools = map[string]bool{
	"whatsapp_connect":           true,
	"whatsapp_connection_status": true,
	"whatsapp_disconnect":        true,
}

// Tools that only make sense with the OAuth login or the web-form routes,
// neither of which exists in gateway mode. whatsapp_disconnect replaces logout.
var gatewayHiddenTools = []string{
	"whatsapp_instance_logout",
	"whatsapp_instance_list",
	"whatsapp_send_form",
	"whatsapp_get_form",
}

const notConnectedMsg = "No WhatsApp number is connected to your account yet. " +
	"Run whatsapp_connect with your full phone number (country code first) to link one."

// BuildGateway is Build for gateway mode: the account of every call comes from
// the verified owner's binding, never from the session or the arguments.
func BuildGateway(mgr *wa.Manager, g Gateway) *mcp.Server {
	s := Build(mgr)
	s.RemoveTools(gatewayHiddenTools...)
	registerGatewayTools(s, g.Pairing)
	if g.Limiter != nil {
		s.AddReceivingMiddleware(limitSends(g.Limiter))
	}
	// Added last so it runs first: everything after it sees the resolved account.
	s.AddReceivingMiddleware(resolveOwner(g.Accounts))
	return s
}

// resolveOwner reads the owner from this request's verified token and binds
// the call to that owner's number. It overwrites any account already in the
// context, so a session can never carry another caller's number.
func resolveOwner(accounts AccountResolver) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			call, ok := req.(*mcp.CallToolRequest)
			if !ok || call.Params == nil {
				return nil, errors.New("malformed tool call")
			}
			owner := ""
			if extra := call.GetExtra(); extra != nil && extra.TokenInfo != nil {
				owner = extra.TokenInfo.UserID
			}
			if owner == "" {
				return errorResult("unauthenticated request"), nil
			}
			account, err := accounts.AccountOf(ctx, owner)
			if err != nil {
				return errorResult("could not look up your WhatsApp connection, try again"), nil
			}
			ctx = authctx.WithAccount(authctx.WithOwner(ctx, owner), account)
			if account == "" && !accountFreeTools[call.Params.Name] {
				return errorResult(notConnectedMsg), nil
			}
			return next(ctx, method, req)
		}
	}
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

type connectArgs struct {
	PhoneNumber string `json:"phone_number" jsonschema:"Your WhatsApp number in international format, country code first (e.g. 5511912345678)"`
}

func registerGatewayTools(s *mcp.Server, p PairingService) {
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_connect", Description: "Link your WhatsApp number to this assistant. " +
		"Returns an 8-digit code: on the phone open WhatsApp > Linked devices > Link a device > " +
		"Link with phone number instead, and type it. The code expires in a few minutes."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in connectArgs) (*mcp.CallToolResult, any, error) {
			res, err := p.Connect(ctx, authctx.Owner(ctx), in.PhoneNumber)
			return done(res, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_connection_status", Description: "Show whether your WhatsApp number " +
		"is linked and connected, and the state of a pairing in progress."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			res, err := p.Status(ctx, authctx.Owner(ctx))
			return done(res, err)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "whatsapp_disconnect", Description: "Unlink your WhatsApp number and permanently " +
		"delete everything stored for it here (messages, chats, contacts, session keys). Cannot be undone."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
			res, err := p.Disconnect(ctx, authctx.Owner(ctx))
			if err != nil {
				return done(nil, fmt.Errorf("disconnect: %w", err))
			}
			return done(res, nil)
		})
}
