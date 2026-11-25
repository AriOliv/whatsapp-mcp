package safehttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestIsBlocked(t *testing.T) {
	cases := []struct {
		addr    string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"0.0.0.0", true},
		{"255.255.255.255", true},
		{"224.0.0.1", true},
		{"::", true},
		{"::1", true},
		{"fd00::1", true},
		{"fe80::1", true},
		{"::ffff:127.0.0.1", true},
		{"::ffff:169.254.169.254", true},
		{"64:ff9b::a9fe:a9fe", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
	}
	for _, c := range cases {
		if got := IsBlocked(netip.MustParseAddr(c.addr)); got != c.blocked {
			t.Errorf("IsBlocked(%s) = %v, want %v", c.addr, got, c.blocked)
		}
	}
}

// trust makes the fetcher accept the test server's certificate, so a refusal
// can only come from the address policy and not from TLS.
func trust(f *Fetcher, srv *httptest.Server) {
	f.client.Transport.(*http.Transport).TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
}

func TestFetchRefusesLoopbackAfterDNS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()
	f := New(Options{})
	trust(f, srv)
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	for _, u := range []string{srv.URL + "/x", "https://localhost:" + port + "/x"} {
		body, err := f.Fetch(context.Background(), u)
		if !errors.Is(err, ErrBlocked) {
			t.Fatalf("Fetch(%s) = %q, %v; want ErrBlocked", u, body, err)
		}
	}
}

func TestFetchURLPolicy(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		url     string
		blocked bool
	}{
		{"plain http", nil, "http://example.com/a.jpg", true},
		{"file scheme", nil, "file:///etc/passwd", true},
		{"userinfo", nil, "https://user:pw@example.com/a.jpg", true},
		{"no allowlist", nil, "https://example.com/a.jpg", false},
		{"exact host allowed", []string{"cdn.example.com"}, "https://cdn.example.com/a.jpg", false},
		{"other host refused", []string{"cdn.example.com"}, "https://evil.example.net/a.jpg", true},
		{"suffix entry", []string{"*.example.com"}, "https://a.b.example.com/a.jpg", false},
		{"suffix does not match lookalike", []string{".example.com"}, "https://badexample.com/a.jpg", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := New(Options{AllowedHosts: c.allowed})
			u, err := url.Parse(c.url)
			if err != nil {
				t.Fatal(err)
			}
			err = f.checkURL(u)
			if c.blocked != errors.Is(err, ErrBlocked) {
				t.Fatalf("checkURL(%s) = %v, want blocked=%v", c.url, err, c.blocked)
			}
		})
	}
}

func TestFetchSizeCapAndRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/small", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(strings.Repeat("x", 64)))
	})
	mux.HandleFunc("/to-http", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/a.jpg", http.StatusFound)
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	f := New(Options{MaxBytes: 16, blockAddr: func(netip.Addr) bool { return false }})
	trust(f, srv)

	if body, err := f.Fetch(context.Background(), srv.URL+"/small"); err != nil || string(body) != "ok" {
		t.Fatalf("small fetch = %q, %v", body, err)
	}
	if _, err := f.Fetch(context.Background(), srv.URL+"/big"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized body must fail, got %v", err)
	}
	if _, err := f.Fetch(context.Background(), srv.URL+"/to-http"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("redirect to http must be refused, got %v", err)
	}
}
