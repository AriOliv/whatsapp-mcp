package oauth

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// maxTrackedIPs bounds the per-IP limiter map; past it the map starts over,
// which at worst grants a burst early.
const maxTrackedIPs = 4096

// ipLimiter is a token bucket per client IP.
type ipLimiter struct {
	every time.Duration
	burst int

	mu  sync.Mutex
	ips map[string]*rate.Limiter
}

func newIPLimiter(perMinute, burst int) *ipLimiter {
	return &ipLimiter{every: time.Minute / time.Duration(perMinute), burst: burst, ips: map[string]*rate.Limiter{}}
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.ips[ip]
	if b == nil {
		if len(l.ips) >= maxTrackedIPs {
			l.ips = map[string]*rate.Limiter{}
		}
		b = rate.NewLimiter(rate.Every(l.every), l.burst)
		l.ips[ip] = b
	}
	return b.Allow()
}

// clientIP is the TCP peer address. Forwarding headers are ignored: on a
// private network the peer is the client, and the headers are client-set.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func tooMany(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	http.Error(w, "too many requests", http.StatusTooManyRequests)
}
