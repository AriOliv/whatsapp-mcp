// Package safehttp fetches user-supplied URLs without letting them reach the
// server's own network: https only, every dialed address checked after DNS
// resolution, bounded time and size, and an optional host allowlist.
package safehttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Defaults for a Fetcher built with zero options.
const (
	DefaultMaxBytes = 20 << 20
	DefaultTimeout  = 30 * time.Second
	maxRedirects    = 3
)

// ErrBlocked is returned when a URL, redirect or resolved address is refused.
var ErrBlocked = errors.New("url not allowed")

// Fetcher downloads https URLs under the policy described in the package doc.
type Fetcher struct {
	client   *http.Client
	maxBytes int64
	allowed  []string
}

// Options configures a Fetcher. Zero values use the package defaults.
type Options struct {
	MaxBytes int64
	Timeout  time.Duration
	// AllowedHosts, when non-empty, restricts fetches to these hosts. An entry
	// starting with "." (or "*.") also matches any subdomain.
	AllowedHosts []string
	// blockAddr overrides the address policy; tests use it to reach a loopback
	// server. Production code never sets it.
	blockAddr func(netip.Addr) bool
}

// New builds a Fetcher.
func New(opts Options) *Fetcher {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	blocked := opts.blockAddr
	if blocked == nil {
		blocked = IsBlocked
	}
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: unparseable address %q", ErrBlocked, address)
			}
			if blocked(ap.Addr()) {
				return fmt.Errorf("%w: %s is not a public address", ErrBlocked, ap.Addr())
			}
			return nil
		},
	}
	allowed := normalizeHosts(opts.AllowedHosts)
	f := &Fetcher{maxBytes: opts.MaxBytes, allowed: allowed}
	f.client = &http.Client{
		Timeout: opts.Timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("%w: too many redirects", ErrBlocked)
			}
			return f.checkURL(req.URL)
		},
	}
	return f
}

// Fetch downloads rawURL and returns its body, failing if it exceeds MaxBytes.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBlocked, err)
	}
	if err := f.checkURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch media: unexpected status %d", resp.StatusCode)
	}
	if resp.ContentLength > f.maxBytes {
		return nil, fmt.Errorf("fetch media: %d bytes is over the %d byte limit", resp.ContentLength, f.maxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch media: %w", err)
	}
	if int64(len(body)) > f.maxBytes {
		return nil, fmt.Errorf("fetch media: body is over the %d byte limit", f.maxBytes)
	}
	return body, nil
}

func (f *Fetcher) checkURL(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("%w: only https URLs are accepted", ErrBlocked)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials in URL", ErrBlocked)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("%w: missing host", ErrBlocked)
	}
	if len(f.allowed) > 0 && !hostAllowed(host, f.allowed) {
		return fmt.Errorf("%w: host %q is not in the allowlist", ErrBlocked, host)
	}
	return nil
}

func normalizeHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		h = strings.TrimPrefix(h, "*")
		if h != "" && h != "." {
			out = append(out, h)
		}
	}
	return out
}

func hostAllowed(host string, allowed []string) bool {
	for _, a := range allowed {
		if strings.HasPrefix(a, ".") {
			if strings.HasSuffix(host, a) || host == a[1:] {
				return true
			}
			continue
		}
		if host == a {
			return true
		}
	}
	return false
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

// IsBlocked reports whether addr must never be dialed for a user-supplied URL:
// loopback, private, link-local (including the cloud metadata address),
// CGNAT, unique-local, unspecified, multicast, and reserved or translation
// ranges that can lead back to any of those.
func IsBlocked(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
