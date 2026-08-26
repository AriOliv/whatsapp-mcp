// Package config loads runtime configuration from the environment.
package config

import (
	"os"
	"strings"
)

type Mode string

const (
	ModeStdio Mode = "stdio"
	ModeHTTP  Mode = "http"
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
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load reads configuration from the environment, applying sane defaults.
func Load() Config {
	mode := Mode(strings.ToLower(env("MCP_MODE", "stdio")))
	if mode != ModeHTTP {
		mode = ModeStdio
	}
	return Config{
		Mode:       mode,
		DBURL:      env("DATABASE_URL", "file:whatsapp.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"),
		Port:       env("PORT", "3000"),
		PublicURL:  strings.TrimRight(env("PUBLIC_URL", "http://localhost:3000"), "/"),
		JWTSecret:  os.Getenv("MCP_JWT_SECRET"),
		DeviceName: env("WA_DEVICE_NAME", "Avenia WhatsApp MCP"),
	}
}

// IsPostgres reports whether the configured DB URL targets Postgres.
func (c Config) IsPostgres() bool {
	return strings.HasPrefix(c.DBURL, "postgres://") || strings.HasPrefix(c.DBURL, "postgresql://")
}
