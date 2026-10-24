package oauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const testJWTSecret = "0123456789abcdef0123456789abcdef"

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRefreshRotationGraceAndIdempotency(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	st := NewStore(testJWTSecret, "https://whatsapp.avenia.tech", "https://whatsapp.avenia.tech/mcp", db, func(string) bool { return true })
	st.nowFn = func() time.Time { return now }
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	initial, err := st.Issue(ctx, "wa_client", "5511999999999", "", []string{"mcp:tools"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// First rotation at t0.
	rot1, err := st.RotateRefresh(ctx, initial.RefreshToken)
	if err != nil {
		t.Fatalf("first RotateRefresh: %v", err)
	}
	if rot1.RefreshToken == initial.RefreshToken {
		t.Fatalf("expected rotated refresh token to differ from initial")
	}
	if rot1.AccessToken == initial.AccessToken {
		t.Fatalf("expected rotated access token to differ from initial")
	}

	// Replay the SAME refresh token at +5s and at the exact +120s boundary:
	// must return the identical successor pair without invalid_grant.
	for _, delta := range []time.Duration{5 * time.Second, 60 * time.Second, 120 * time.Second} {
		now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC).Add(delta)
		replay, err := st.RotateRefresh(ctx, initial.RefreshToken)
		if err != nil {
			t.Fatalf("replay at +%s failed: %v", delta, err)
		}
		if replay.RefreshToken != rot1.RefreshToken || replay.AccessToken != rot1.AccessToken {
			t.Fatalf("replay at +%s returned different pair: got (%s, %s), want (%s, %s)",
				delta, replay.AccessToken, replay.RefreshToken, rot1.AccessToken, rot1.RefreshToken)
		}
	}

	// The successor refresh token itself must still be active and rotatable.
	rot2, err := st.RotateRefresh(ctx, rot1.RefreshToken)
	if err != nil {
		t.Fatalf("rotating successor token failed: %v", err)
	}
	if rot2.RefreshToken == rot1.RefreshToken {
		t.Fatalf("expected second rotation to mint a new refresh token")
	}
}

func TestRefreshConcurrentProcessesReceiveSameSuccessor(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	st := NewStore(testJWTSecret, "https://whatsapp.avenia.tech", "https://whatsapp.avenia.tech/mcp", db, func(string) bool { return true })
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	initial, err := st.Issue(ctx, "wa_client", "5511999999999", "", []string{"mcp:tools"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	mux := http.NewServeMux()
	h := NewHandlers(st, nil, "https://whatsapp.avenia.tech")
	h.Register(mux, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	const workers = 4
	type result struct {
		status  int
		access  string
		refresh string
		errCode string
	}
	results := make([]result, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			form := url.Values{
				"grant_type":    {"refresh_token"},
				"refresh_token": {initial.RefreshToken},
			}
			resp, err := http.PostForm(srv.URL+"/token", form)
			if err != nil {
				t.Errorf("worker %d post: %v", idx, err)
				return
			}
			defer resp.Body.Close()
			var body map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&body)
			results[idx] = result{
				status:  resp.StatusCode,
				access:  strVal(body["access_token"]),
				refresh: strVal(body["refresh_token"]),
				errCode: strVal(body["error"]),
			}
		}(i)
	}
	close(start)
	wg.Wait()

	for i, r := range results {
		if r.status != http.StatusOK {
			t.Fatalf("worker %d got status %d (error=%q), want 200", i, r.status, r.errCode)
		}
		if r.refresh == "" || r.access == "" {
			t.Fatalf("worker %d got empty tokens: %+v", i, r)
		}
		if r.refresh != results[0].refresh || r.access != results[0].access {
			t.Fatalf("worker %d got pair (%s, %s), want (%s, %s)",
				i, r.access, r.refresh, results[0].access, results[0].refresh)
		}
	}
}

func TestRefreshReuseOutsideGraceRevokesFamily(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	st := NewStore(testJWTSecret, "https://whatsapp.avenia.tech", "https://whatsapp.avenia.tech/mcp", db, func(string) bool { return true })
	st.nowFn = func() time.Time { return now }
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	famA, err := st.Issue(ctx, "wa_client", "5511999999991", "", []string{"mcp:tools"})
	if err != nil {
		t.Fatalf("Issue famA: %v", err)
	}
	famB, err := st.Issue(ctx, "wa_client", "5511999999992", "", []string{"mcp:tools"})
	if err != nil {
		t.Fatalf("Issue famB: %v", err)
	}

	// Rotate famA twice: R1 -> R2 -> R3.
	rot1, err := st.RotateRefresh(ctx, famA.RefreshToken)
	if err != nil {
		t.Fatalf("RotateRefresh R1: %v", err)
	}
	now = now.Add(10 * time.Second)
	rot2, err := st.RotateRefresh(ctx, rot1.RefreshToken)
	if err != nil {
		t.Fatalf("RotateRefresh R2: %v", err)
	}

	// Advance past R1's 120s grace window (121s since R1 was consumed).
	now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC).Add(121 * time.Second)
	if _, err := st.RotateRefresh(ctx, famA.RefreshToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("replay of R1 outside grace: got err=%v, want ErrInvalidGrant", err)
	}

	// Both R2 and R3 in famA must now be revoked.
	if _, err := st.RotateRefresh(ctx, rot1.RefreshToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("expected R2 to be revoked with family, got err=%v", err)
	}
	if _, err := st.RotateRefresh(ctx, rot2.RefreshToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("expected R3 to be revoked with family, got err=%v", err)
	}

	// Unrelated family famB must remain untouched.
	if _, err := st.RotateRefresh(ctx, famB.RefreshToken); err != nil {
		t.Fatalf("unrelated family famB should still rotate cleanly: %v", err)
	}
}

func TestRefreshMigrationPreservesLegacyRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Simulate pre-existing 6-column schema and insert a legacy refresh token row.
	if _, err := db.ExecContext(ctx, `CREATE TABLE oauth_refresh_tokens (
		token      TEXT PRIMARY KEY,
		client_id  TEXT,
		sub        TEXT,
		resource   TEXT,
		scopes     TEXT,
		expires_at BIGINT
	)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	t0 := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO oauth_refresh_tokens (token, client_id, sub, resource, scopes, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		"legacy_tok_1", "wa_legacy", "5511888888888", "https://whatsapp.avenia.tech/mcp", `["mcp:tools"]`,
		t0.Add(RefreshTTL).UnixMilli(),
	); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	now := t0
	st := NewStore(testJWTSecret, "https://whatsapp.avenia.tech", "https://whatsapp.avenia.tech/mcp", db, func(string) bool { return true })
	st.nowFn = func() time.Time { return now }
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init migration on legacy table: %v", err)
	}

	// First rotation of legacy row succeeds.
	succ, err := st.RotateRefresh(ctx, "legacy_tok_1")
	if err != nil {
		t.Fatalf("RotateRefresh legacy row: %v", err)
	}

	// Replay within 120s returns same successor.
	now = t0.Add(30 * time.Second)
	replay, err := st.RotateRefresh(ctx, "legacy_tok_1")
	if err != nil {
		t.Fatalf("replay legacy row within grace: %v", err)
	}
	if replay.RefreshToken != succ.RefreshToken || replay.AccessToken != succ.AccessToken {
		t.Fatalf("legacy replay mismatch: got %+v, want %+v", replay, succ)
	}

	// Replay outside 120s revokes the successor minted from the legacy row.
	now = t0.Add(125 * time.Second)
	if _, err := st.RotateRefresh(ctx, "legacy_tok_1"); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("expected ErrInvalidGrant outside grace, got %v", err)
	}
	if _, err := st.RotateRefresh(ctx, succ.RefreshToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("expected successor of legacy row to be revoked on reuse, got %v", err)
	}
}

func TestRefreshExpiredAndUnlinkedDevice(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	t0 := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	now := t0
	paired := true
	st := NewStore(testJWTSecret, "https://whatsapp.avenia.tech", "https://whatsapp.avenia.tech/mcp", db, func(string) bool { return paired })
	st.nowFn = func() time.Time { return now }
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// 1. Expired refresh token fails with ErrInvalidGrant.
	tokExp, err := st.Issue(ctx, "wa_client", "5511999999999", "", []string{"mcp:tools"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	now = t0.Add(RefreshTTL + time.Minute)
	if _, err := st.RotateRefresh(ctx, tokExp.RefreshToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("expired refresh: got %v, want ErrInvalidGrant", err)
	}

	// 2. hasDevice=false denies the grant without revoking the family (both on
	// first consumption and during grace replay), so a transient absence never
	// logs out active clients once the device is present again.
	now = t0
	tokDevice, err := st.Issue(ctx, "wa_client", "5511999999999", "", []string{"mcp:tools"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	paired = false
	if _, err := st.RotateRefresh(ctx, tokDevice.RefreshToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("unlinked device refresh: got %v, want ErrInvalidGrant", err)
	}
	paired = true
	succ, err := st.RotateRefresh(ctx, tokDevice.RefreshToken)
	if err != nil {
		t.Fatalf("refresh after device returns should succeed (family not revoked): %v", err)
	}
	// During grace window, hasDevice=false also denies without revoking the family.
	now = t0.Add(15 * time.Second)
	paired = false
	if _, err := st.RotateRefresh(ctx, tokDevice.RefreshToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("grace replay while hasDevice=false: got %v, want ErrInvalidGrant", err)
	}
	paired = true
	replay, err := st.RotateRefresh(ctx, tokDevice.RefreshToken)
	if err != nil {
		t.Fatalf("grace replay after device returns: %v", err)
	}
	if replay.RefreshToken != succ.RefreshToken {
		t.Fatalf("expected same successor after transient hasDevice=false, got %s want %s", replay.RefreshToken, succ.RefreshToken)
	}
	if _, err := st.RotateRefresh(ctx, succ.RefreshToken); err != nil {
		t.Fatalf("successor token should remain valid after transient hasDevice=false: %v", err)
	}
}

func TestRefreshPurge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := newTestDB(t)

	t0 := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	now := t0
	st := NewStore(testJWTSecret, "https://whatsapp.avenia.tech", "https://whatsapp.avenia.tech/mcp", db, func(string) bool { return true })
	st.nowFn = func() time.Time { return now }
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Issue and rotate a token at t0 -> creates consumed row (tok1) + active successor (succ1).
	tok1, err := st.Issue(ctx, "wa_client", "5511999999991", "", []string{"mcp:tools"})
	if err != nil {
		t.Fatalf("Issue tok1: %v", err)
	}
	succ1, err := st.RotateRefresh(ctx, tok1.RefreshToken)
	if err != nil {
		t.Fatalf("RotateRefresh tok1: %v", err)
	}

	// Issue an unconsumed token whose expires_at will be in the past when we purge at t0 + 181s.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO oauth_refresh_tokens (token, client_id, sub, resource, scopes, expires_at, family_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		"expired_unconsumed", "wa_client", "5511999999992", "https://whatsapp.avenia.tech/mcp", `["mcp:tools"]`,
		t0.Add(60*time.Second).UnixMilli(), "expired_unconsumed",
	); err != nil {
		t.Fatalf("insert expired_unconsumed: %v", err)
	}

	// At t0 + 150s (within RefreshGrace + 60s = 180s): tok1 must NOT be purged yet,
	// while expired_unconsumed (expired at +60s) MUST be purged.
	now = t0.Add(150 * time.Second)
	n, err := st.Purge(ctx)
	if err != nil {
		t.Fatalf("Purge at +150s: %v", err)
	}
	if n != 1 {
		t.Fatalf("Purge at +150s deleted %d row(s), want 1 (only expired_unconsumed)", n)
	}

	// At t0 + 181s (> RefreshGrace + 60s): tok1 is purged, while active succ1 stays.
	now = t0.Add(RefreshGrace + RefreshPurgeSlack + time.Second)
	n, err = st.Purge(ctx)
	if err != nil {
		t.Fatalf("Purge at +181s: %v", err)
	}
	if n != 1 {
		t.Fatalf("Purge at +181s deleted %d row(s), want 1 (consumed tok1)", n)
	}

	// Active successor succ1 is untouched and still rotates cleanly.
	if _, err := st.RotateRefresh(ctx, succ1.RefreshToken); err != nil {
		t.Fatalf("active successor succ1 should remain rotatable after Purge: %v", err)
	}
}

func TestBearerGuard401AndResourceMetadataOnExpiredToken(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	t0 := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	now := t0
	st := NewStore(testJWTSecret, "https://whatsapp.avenia.tech", "https://whatsapp.avenia.tech/mcp", db, func(string) bool { return true })
	st.nowFn = func() time.Time { return now }
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	toks, err := st.Issue(ctx, "wa_client", "5511999999999", "", []string{"mcp:tools"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	mux := http.NewServeMux()
	h := NewHandlers(st, nil, "https://whatsapp.avenia.tech")
	h.Register(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"sub": SubFromContext(r.Context())})
	}))

	const wantWWW = `Bearer resource_metadata="https://whatsapp.avenia.tech/.well-known/oauth-protected-resource/mcp"`

	// 1. Missing token -> 401 + WWW-Authenticate resource_metadata.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status=%d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != wantWWW {
		t.Fatalf("no token WWW-Authenticate=%q, want %q", got, wantWWW)
	}

	// 2. Valid token at t0 -> 200 OK.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+toks.AccessToken)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token: status=%d, want 200", rec.Code)
	}

	// 3. Expired token (t0 + AccessTTL + 1m) -> 401 + WWW-Authenticate resource_metadata.
	now = t0.Add(AccessTTL + time.Minute)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+toks.AccessToken)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired token: status=%d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != wantWWW {
		t.Fatalf("expired token WWW-Authenticate=%q, want %q", got, wantWWW)
	}
	if !strings.Contains(rec.Body.String(), `"unauthorized"`) {
		t.Fatalf("expired token body=%q, want unauthorized JSON", rec.Body.String())
	}
}

func strVal(v any) string {
	s, _ := v.(string)
	return s
}
