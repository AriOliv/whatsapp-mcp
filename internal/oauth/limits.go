package oauth

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
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

// clientIP is the address limits are keyed on. With trustHops == 0 it is the
// TCP peer and X-Forwarded-For is ignored, since a client can set it to
// anything. Behind trustHops proxies that each append the address they saw,
// it is the X-Forwarded-For entry that many places from the right: the one
// written by the outermost trusted proxy. A missing or short header falls
// back to the peer.
func clientIP(r *http.Request, trustHops int) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	if trustHops <= 0 {
		return peer
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(v, ",") {
			hops = append(hops, strings.TrimSpace(part))
		}
	}
	if len(hops) < trustHops {
		return peer
	}
	addr, err := netip.ParseAddr(hops[len(hops)-trustHops])
	if err != nil {
		return peer
	}
	return addr.Unmap().String()
}

func tooMany(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	http.Error(w, "too many requests", http.StatusTooManyRequests)
}
