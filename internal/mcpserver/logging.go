package mcpserver

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// slowToolCall is how long a tool may take before it is flagged as slow. Tool
// calls compete with message reception for the same database, so one that drags
// is the first sign of trouble.
const slowToolCall = 5 * time.Second

// logToolCalls records a structured log line for every tools/call invocation:
// tool name, masked account (never full phone numbers, payloads, or tokens),
// duration, and status/error.
//
// A handler's error is returned to the client inside the result and never
// reaches the server log on its own, so without this middleware a report of
// "tools are failing" leaves nothing in the pod logs to diagnose.
func logToolFailures(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		name := "unknown"
		if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
			name = call.Params.Name
		}
		account := maskAccount(acct(ctx))
		start := time.Now()
		res, err := next(ctx, method, req)
		took := time.Since(start).Round(time.Millisecond)

		switch {
		case err != nil:
			log.Printf("tools/call tool=%s account=%s duration=%s status=error err=%q", name, account, took, err.Error())
		case isErrorResult(res):
			log.Printf("tools/call tool=%s account=%s duration=%s status=tool_error err=%q", name, account, took, resultText(res))
		case took > slowToolCall:
			log.Printf("tools/call tool=%s account=%s duration=%s status=ok slow=true", name, account, took)
		default:
			log.Printf("tools/call tool=%s account=%s duration=%s status=ok", name, account, took)
		}
		return res, err
	}
}

// maskAccount redacts the middle digits of an account phone number (e.g.
// "5521967582535" -> "5521…2535") so logs can correlate per-account failures
// without exposing full numbers.
func maskAccount(a string) string {
	a = strings.TrimSpace(a)
	if a == "" {
		return "default"
	}
	if len(a) <= 6 {
		return "***"
	}
	return a[:4] + "…" + a[len(a)-4:]
}

func isErrorResult(res mcp.Result) bool {
	call, ok := res.(*mcp.CallToolResult)
	return ok && call.IsError
}

// resultText returns the message a failed tool reported, so the log says what
// went wrong rather than only that something did.
func resultText(res mcp.Result) string {
	call, ok := res.(*mcp.CallToolResult)
	if !ok {
		return ""
	}
	for _, c := range call.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			return t.Text
		}
	}
	return ""
}
