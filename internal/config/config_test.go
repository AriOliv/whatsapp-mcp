package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvOrFile(t *testing.T) {
	dir := t.TempDir()
	dsnFile := filepath.Join(dir, "dsn")
	if err := os.WriteFile(dsnFile, []byte("  postgres://u:p@db:5432/wa?sslmode=require\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	blank := filepath.Join(dir, "blank")
	if err := os.WriteFile(blank, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{"env wins over file", map[string]string{"DATABASE_URL": "postgres://env", "DATABASE_URL_FILE": dsnFile}, "postgres://env", false},
		{"file is read and trimmed", map[string]string{"DATABASE_URL_FILE": dsnFile}, "postgres://u:p@db:5432/wa?sslmode=require", false},
		{"neither falls back to sqlite", map[string]string{}, defaultDBURL, false},
		{"missing file fails", map[string]string{"DATABASE_URL_FILE": filepath.Join(dir, "nope")}, "", true},
		{"empty file fails", map[string]string{"DATABASE_URL_FILE": blank}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := envOrFile(func(k string) string { return c.env[k] }, os.ReadFile, "DATABASE_URL", defaultDBURL)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestJWTSecretFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jwt")
	if err := os.WriteFile(path, []byte("\n  0123456789abcdef0123456789abcdef  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_JWT_SECRET", "")
	t.Setenv("MCP_JWT_SECRET_FILE", path)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JWTSecret != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("JWTSecret = %q, want the trimmed file contents", cfg.JWTSecret)
	}
	t.Setenv("MCP_JWT_SECRET", "from-env-0123456789abcdef0123456789")
	if cfg, _ := Load(); cfg.JWTSecret != "from-env-0123456789abcdef0123456789" {
		t.Fatalf("MCP_JWT_SECRET must win over the file, got %q", cfg.JWTSecret)
	}
}

func TestFormsOffAndClaudeRedirectHostsByDefault(t *testing.T) {
	t.Setenv("FORMS_ENABLED", "")
	t.Setenv("OAUTH_ALLOWED_REDIRECT_HOSTS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FormsEnabled {
		t.Fatal("forms must be off unless FORMS_ENABLED=true")
	}
	if len(cfg.OAuthRedirectHosts) != 2 || cfg.OAuthRedirectHosts[0] != "claude.ai" || cfg.OAuthRedirectHosts[1] != "claude.com" {
		t.Fatalf("default redirect hosts = %v", cfg.OAuthRedirectHosts)
	}
	t.Setenv("FORMS_ENABLED", "true")
	t.Setenv("OAUTH_ALLOWED_REDIRECT_HOSTS", " Example.com , ")
	cfg, _ = Load()
	if !cfg.FormsEnabled || len(cfg.OAuthRedirectHosts) != 1 || cfg.OAuthRedirectHosts[0] != "example.com" {
		t.Fatalf("explicit settings not applied: forms=%v hosts=%v", cfg.FormsEnabled, cfg.OAuthRedirectHosts)
	}
}

func TestTrustProxyHops(t *testing.T) {
	for _, c := range []struct {
		env  string
		want int
	}{{"", 0}, {"1", 1}, {"2", 2}, {"-1", 0}, {"x", 0}} {
		t.Setenv("TRUST_PROXY_HOPS", c.env)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.TrustProxyHops != c.want {
			t.Errorf("TRUST_PROXY_HOPS=%q -> %d, want %d", c.env, cfg.TrustProxyHops, c.want)
		}
	}
}
