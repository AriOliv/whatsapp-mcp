package oauth

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// DefaultRedirectHosts are the https hosts a registered client may redirect to
// when OAUTH_ALLOWED_REDIRECT_HOSTS is unset.
var DefaultRedirectHosts = []string{"claude.ai", "claude.com"}

const (
	maxRedirectURIs   = 10
	maxRedirectURILen = 2048
)

var loopbackHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

// validateRedirectURIs checks a DCR redirect_uris list: non-empty, bounded, and
// every entry acceptable to validateRedirectURI.
func validateRedirectURIs(uris []string, httpsHosts []string) error {
	if len(uris) == 0 {
		return errors.New("redirect_uris is required")
	}
	if len(uris) > maxRedirectURIs {
		return fmt.Errorf("at most %d redirect_uris", maxRedirectURIs)
	}
	for _, u := range uris {
		if err := validateRedirectURI(u, httpsHosts); err != nil {
			return err
		}
	}
	return nil
}

// validateRedirectURI accepts http only on a loopback host with an explicit
// port (native clients, RFC 8252) and https only on an allowed host. No
// fragment, no userinfo, no other scheme.
func validateRedirectURI(raw string, httpsHosts []string) error {
	if raw == "" || len(raw) > maxRedirectURILen || strings.ContainsAny(raw, "# \t\r\n") {
		return fmt.Errorf("redirect_uri %q is not allowed", raw)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("redirect_uri %q is not allowed", raw)
	}
	host := strings.ToLower(u.Hostname())
	switch u.Scheme {
	case "http":
		if !loopbackHosts[host] || u.Port() == "" {
			return fmt.Errorf("http redirect_uri must be a loopback address with a port: %q", raw)
		}
	case "https":
		if !containsHost(httpsHosts, host) {
			return fmt.Errorf("redirect_uri host %q is not allowed", host)
		}
	default:
		return fmt.Errorf("redirect_uri scheme %q is not allowed", u.Scheme)
	}
	return nil
}

func containsHost(hosts []string, host string) bool {
	for _, h := range hosts {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}
	return false
}
