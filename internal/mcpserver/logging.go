package mcpserver

import (
	"context"
	"log"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// slowToolCall is how long a tool may take before it is worth a log line. Tool
// calls compete with message reception for the same database, so one that drags
// is the first sign of trouble.
const slowToolCall = 5 * time.Second

// logToolFailures records tool calls that fail or run long.
//
// A handler's error is returned to the client inside the result and never
// reaches the server log, so a report of "the tools are erroring" leaves nothing
// to investigate afterwards. This middleware closes that gap: it names the tool,
// says how long it took, and reports both a transport-level error and the
// in-result IsError that the SDK treats as a successful response.
//
// Arguments are deliberately not logged — they carry phone numbers and message
// text.
func logToolFailures(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		name := "unknown"
		if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
			name = call.Params.Name
		}
		start := time.Now()
		res, err := next(ctx, method, req)
		took := time.Since(start)

		switch {
		case err != nil:
			log.Printf("tool %s failed after %s: %v", name, took, err)
		case isErrorResult(res):
			log.Printf("tool %s returned an error after %s: %s", name, took, resultText(res))
		case took > slowToolCall:
			log.Printf("tool %s took %s", name, took)
		}
		return res, err
	}
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
