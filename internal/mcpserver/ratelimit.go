package mcpserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

// sendTools are the calls that put a message (or a visible change) in front of
// other people. WhatsApp bans numbers that send too much too fast, so these
// share one per-account budget.
var sendTools = map[string]bool{
	"whatsapp_send_text":                true,
	"whatsapp_send_media":               true,
	"whatsapp_send_audio":               true,
	"whatsapp_send_reaction":            true,
	"whatsapp_send_sticker":             true,
	"whatsapp_send_location":            true,
	"whatsapp_send_contact":             true,
	"whatsapp_send_poll":                true,
	"whatsapp_send_buttons":             true,
	"whatsapp_send_carousel":            true,
	"whatsapp_send_form":                true,
	"whatsapp_send_flow":                true,
	"whatsapp_send_list":                true,
	"whatsapp_update_message":           true,
	"whatsapp_group_create":             true,
	"whatsapp_group_update_participant": true,
}

// SendLimiter is a token bucket per account.
type SendLimiter struct {
	perMinute int
	burst     int
	now       func() time.Time

	mu      sync.Mutex
	buckets map[string]*rate.Limiter
}

// NewSendLimiter allows perMinute sends per account on average, with bursts of
// up to burst.
func NewSendLimiter(perMinute, burst int) *SendLimiter {
	return &SendLimiter{perMinute: perMinute, burst: burst, now: time.Now, buckets: map[string]*rate.Limiter{}}
}

// allow takes one send from account's budget, or reports how long to wait.
func (l *SendLimiter) allow(account string) (bool, time.Duration) {
	l.mu.Lock()
	b := l.buckets[account]
	if b == nil {
		b = rate.NewLimiter(rate.Limit(float64(l.perMinute)/60), l.burst)
		l.buckets[account] = b
	}
	l.mu.Unlock()
	now := l.now()
	r := b.ReserveN(now, 1)
	if !r.OK() {
		return false, time.Minute
	}
	if wait := r.DelayFrom(now); wait > 0 {
		r.CancelAt(now)
		return false, wait
	}
	return true, 0
}

// LimitSends adds the per-account send budget to s.
func LimitSends(s *mcp.Server, l *SendLimiter) {
	s.AddReceivingMiddleware(limitSends(l))
}

func limitSends(l *SendLimiter) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			call, ok := req.(*mcp.CallToolRequest)
			if method != "tools/call" || !ok || call.Params == nil || !sendTools[call.Params.Name] {
				return next(ctx, method, req)
			}
			if ok, wait := l.allow(acct(ctx)); !ok {
				return errorResult(fmt.Sprintf("Send rate limit reached for this WhatsApp number (%d per minute) "+
					"to protect it from being banned. Try again in %s.", l.perMinute, wait.Round(time.Second))), nil
			}
			return next(ctx, method, req)
		}
	}
}
