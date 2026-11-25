package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseURL(t *testing.T) {
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
			got, err := databaseURL(func(k string) string { return c.env[k] }, os.ReadFile)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
