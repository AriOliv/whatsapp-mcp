// Package config loads runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Mode string

const (
	ModeStdio Mode = "stdio"
	ModeHTTP  Mode = "http"
)

// AuthMode picks how HTTP callers are authenticated.
type AuthMode string

const (
	// AuthOAuth is the built-in OAuth server with WhatsApp pairing as login.
	AuthOAuth AuthMode = "oauth"
	// AuthGateway trusts a JWT minted per request by an MCP gateway.
	AuthGateway AuthMode = "gateway"
)

// Config holds all runtime settings for the WhatsApp MCP server.
type Config struct {
	Mode Mode
	// DBURL selects the store. postgres://... uses Postgres (recommended for
	// multi-account HTTP mode); anything else is treated as a modernc SQLite DSN
	// (pure-Go, no CGO) — handy for local stdio use.
	DBURL string
	// Port/PublicURL/JWTSecret are only used in HTTP mode.
	Port      string
	PublicURL string
	JWTSecret string
	// DeviceName is what shows up under "Linked devices" in the WhatsApp app.
	// Set at pairing time via store.DeviceProps.Os; renaming needs a re-link.
	DeviceName string
	// Inbound webhook + gateway REST API (both optional): one paired number used
	// as a service's front door. Accounts are phone digits, comma-separated.
	InboundWebhookURL      string
	InboundWebhookSecret   string
	InboundWebhookAccounts string
	GatewayAPIToken        string

	AuthMode AuthMode
	// Gateway mode (MCP_AUTH_MODE=gateway).
	GatewayJWKSURL      string
	GatewayJWTIssuer    string
	GatewayJWTAudience  string
	AllowedEmailDomains string
	MediaAllowedHosts   string
	SendRatePerMinute   int
	SendRateBurst       int

	// FormsEnabled mounts /f/* (web forms sent over WhatsApp). Off by default.
	FormsEnabled bool
	// OAuthRedirectHosts are the https hosts DCR clients may redirect to.
	OAuthRedirectHosts []string
}

const defaultDBURL = "file:whatsapp.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"

// envOrFile returns $key, else the trimmed contents of the file named by
// ${key}_FILE (secrets mounted as files), else def.
func envOrFile(getenv func(string) string, readFile func(string) ([]byte, error), key, def string) (string, error) {
	if v := getenv(key); v != "" {
		return v, nil
	}
	path := getenv(key + "_FILE")
	if path == "" {
		return def, nil
	}
	b, err := readFile(path)
	if err != nil {
		return "", fmt.Errorf("%s_FILE: %w", key, err)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("%s_FILE %s is empty", key, path)
	}
	return v, nil
}

func csv(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil && n > 0 {
		return n
	}
	return def
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load reads configuration from the environment, applying sane defaults.
func Load() (Config, error) {
	mode := Mode(strings.ToLower(env("MCP_MODE", "stdio")))
	if mode != ModeHTTP {
		mode = ModeStdio
	}
	authMode := AuthMode(strings.ToLower(env("MCP_AUTH_MODE", string(AuthOAuth))))
	if authMode != AuthOAuth && authMode != AuthGateway {
		return Config{}, fmt.Errorf("MCP_AUTH_MODE must be %q or %q, got %q", AuthOAuth, AuthGateway, authMode)
	}
	dbURL, err := envOrFile(os.Getenv, os.ReadFile, "DATABASE_URL", defaultDBURL)
	if err != nil {
		return Config{}, err
	}
	jwtSecret, err := envOrFile(os.Getenv, os.ReadFile, "MCP_JWT_SECRET", "")
	if err != nil {
		return Config{}, err
	}
	redirectHosts := csv(env("OAUTH_ALLOWED_REDIRECT_HOSTS", "claude.ai,claude.com"))
	return Config{
		Mode:       mode,
		DBURL:      dbURL,
		Port:       env("PORT", "3000"),
		PublicURL:  strings.TrimRight(env("PUBLIC_URL", "http://localhost:3000"), "/"),
		JWTSecret:  jwtSecret,
		DeviceName: env("WA_DEVICE_NAME", "WhatsApp MCP"),

		InboundWebhookURL:      os.Getenv("INBOUND_WEBHOOK_URL"),
		InboundWebhookSecret:   os.Getenv("INBOUND_WEBHOOK_SECRET"),
		InboundWebhookAccounts: os.Getenv("INBOUND_WEBHOOK_ACCOUNTS"),
		GatewayAPIToken:        os.Getenv("GATEWAY_API_TOKEN"),

		AuthMode:            authMode,
		GatewayJWKSURL:      os.Getenv("GATEWAY_JWKS_URL"),
		GatewayJWTIssuer:    os.Getenv("GATEWAY_JWT_ISSUER"),
		GatewayJWTAudience:  os.Getenv("GATEWAY_JWT_AUDIENCE"),
		AllowedEmailDomains: os.Getenv("ALLOWED_EMAIL_DOMAINS"),
		MediaAllowedHosts:   os.Getenv("MEDIA_URL_ALLOWED_HOSTS"),
		SendRatePerMinute:   envInt("SEND_RATE_PER_MINUTE", 20),
		SendRateBurst:       envInt("SEND_RATE_BURST", 5),
		FormsEnabled:        strings.EqualFold(strings.TrimSpace(os.Getenv("FORMS_ENABLED")), "true"),
		OAuthRedirectHosts:  redirectHosts,
	}, nil
}

// IsPostgres reports whether the configured DB URL targets Postgres.
func (c Config) IsPostgres() bool {
	return strings.HasPrefix(c.DBURL, "postgres://") || strings.HasPrefix(c.DBURL, "postgresql://")
}
