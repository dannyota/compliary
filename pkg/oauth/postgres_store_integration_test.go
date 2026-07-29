package oauth

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// TestPostgresStorePersistenceAndReplay runs only when explicitly pointed at a
// migrated test database:
//
//	COMPLIARY_OAUTH_TEST_DSN=postgres://... go test ./pkg/oauth -run TestPostgres
func TestPostgresStorePersistenceAndReplay(t *testing.T) {
	dsn := os.Getenv("COMPLIARY_OAUTH_TEST_DSN")
	if dsn == "" {
		t.Skip("COMPLIARY_OAUTH_TEST_DSN is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	srv, err := New("https://oauth-test.example", testHash(t), pool, nil)
	if err != nil {
		t.Fatalf("build PostgreSQL OAuth server: %v", err)
	}
	t.Cleanup(srv.StopEvictor)
	storeA, ok := srv.store.(*postgresStore)
	if !ok {
		t.Fatal("New did not select the PostgreSQL OAuth store")
	}
	storeB := newPostgresStore(pool) // simulates another process after restart
	reg, err := storeA.registerClient(ctx, oauthex.ClientRegistrationMetadata{
		RedirectURIs: []string{"http://localhost/callback"},
		ClientName:   "OAuth persistence integration test",
	})
	if err != nil {
		t.Fatalf("register client: %v", err)
	}

	provider := "integration-" + randomHex(8)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx,
			`DELETE FROM auth.oauth_client WHERE client_id = $1`,
			reg.ClientID,
		)
		_, _ = pool.Exec(cleanupCtx,
			`DELETE FROM auth.identity WHERE provider = $1`,
			provider,
		)
	})

	principal := Principal{
		Provider:      provider,
		Subject:       "subject-1",
		Email:         "USER@Example.COM",
		EmailVerified: true,
	}
	verifier, challenge := pkce()
	code, err := storeA.createCode(
		ctx,
		reg.ClientID,
		"http://localhost/callback",
		challenge,
		"S256",
		"mcp:read",
		principal,
	)
	if err != nil {
		t.Fatalf("create code: %v", err)
	}

	scope, accessToken, refreshToken, expiresIn, ok, err := storeB.exchangeCode(
		ctx, code, reg.ClientID, "http://localhost/callback", verifier,
	)
	if err != nil {
		t.Fatalf("exchange code from second store: %v", err)
	}
	if !ok || scope != "mcp:read" || expiresIn != int(tokenIdleTTL.Seconds()) {
		t.Fatalf("exchange result: ok=%v scope=%q expires_in=%d", ok, scope, expiresIn)
	}

	entry, ok, err := storeA.lookupToken(ctx, accessToken)
	if err != nil {
		t.Fatalf("lookup persisted token: %v", err)
	}
	if !ok {
		t.Fatal("second store's token was not visible to first store")
	}
	if entry.Principal.Provider != provider ||
		entry.Principal.Subject != principal.Subject ||
		entry.Principal.Email != strings.ToLower(principal.Email) ||
		!entry.Principal.EmailVerified {
		t.Errorf("persisted principal: got %+v", entry.Principal)
	}
	if entry.ExpiresAt.Before(time.Now().Add(tokenIdleTTL - time.Minute)) {
		t.Errorf("sliding expiry was not renewed: %v", entry.ExpiresAt)
	}

	var storedHash []byte
	if err := pool.QueryRow(ctx, `
SELECT token_hash
FROM auth.access_token
WHERE family_id = $1 AND token_hash = $2`,
		entry.FamilyID, hashToken(accessToken),
	).Scan(&storedHash); err != nil {
		t.Fatalf("read stored token hash: %v", err)
	}
	if !bytes.Equal(storedHash, hashToken(accessToken)) {
		t.Error("access token was not stored by SHA-256 hash")
	}
	if bytes.Equal(storedHash, []byte(accessToken)) {
		t.Error("raw access token was persisted")
	}

	deny := AuthorizerFunc(func(context.Context, Principal) error {
		return errors.New("denied by integration policy")
	})
	if _, _, _, _, ok, err := storeA.rotateRefresh(
		ctx, refreshToken, reg.ClientID, deny,
	); err != nil || ok {
		t.Fatalf("policy-denied refresh: ok=%v err=%v", ok, err)
	}

	// Access renewal and refresh rotation may arrive together when a client
	// reaches its advertised deadline. They must share a consistent
	// family-first lock order and both complete without a deadlock.
	type useResult struct {
		ok  bool
		err error
	}
	type refreshResult struct {
		access string
		ok     bool
		err    error
	}
	start := make(chan struct{})
	used := make(chan useResult, 1)
	refreshed := make(chan refreshResult, 1)
	go func() {
		<-start
		_, ok, err := storeA.renewToken(ctx, accessToken)
		used <- useResult{ok: ok, err: err}
	}()
	go func() {
		<-start
		_, access, _, _, ok, err := storeB.rotateRefresh(
			ctx, refreshToken, reg.ClientID, AllowAllAuthorizer{},
		)
		refreshed <- refreshResult{access: access, ok: ok, err: err}
	}()
	close(start)

	use := <-used
	if use.err != nil || !use.ok {
		t.Fatalf("concurrent access renewal: ok=%v err=%v", use.ok, use.err)
	}
	refresh := <-refreshed
	if refresh.err != nil || !refresh.ok {
		t.Fatalf("concurrent refresh rotation: ok=%v err=%v", refresh.ok, refresh.err)
	}
	// Replaying the consumed ancestor through another store revokes the family.
	if _, _, _, _, ok, err := storeB.rotateRefresh(
		ctx, refreshToken, reg.ClientID, AllowAllAuthorizer{},
	); err != nil || ok {
		t.Fatalf("replay result: ok=%v err=%v", ok, err)
	}
	if _, ok, err := storeA.lookupToken(ctx, refresh.access); err != nil || ok {
		t.Fatalf("token family survived refresh replay: ok=%v err=%v", ok, err)
	}
}

func TestPostgresStoreConcurrentCodeExchange(t *testing.T) {
	dsn := os.Getenv("COMPLIARY_OAUTH_TEST_DSN")
	if dsn == "" {
		t.Skip("COMPLIARY_OAUTH_TEST_DSN is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	storeA := newPostgresStore(pool)
	storeB := newPostgresStore(pool)
	reg, err := storeA.registerClient(ctx, oauthex.ClientRegistrationMetadata{
		RedirectURIs: []string{"http://localhost/callback"},
		ClientName:   "OAuth concurrent exchange integration test",
	})
	if err != nil {
		t.Fatalf("register client: %v", err)
	}
	provider := "integration-" + randomHex(8)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx,
			`DELETE FROM auth.oauth_client WHERE client_id = $1`,
			reg.ClientID,
		)
		_, _ = pool.Exec(cleanupCtx,
			`DELETE FROM auth.identity WHERE provider = $1`,
			provider,
		)
	})

	verifier, challenge := pkce()
	code, err := storeA.createCode(
		ctx,
		reg.ClientID,
		"http://localhost/callback",
		challenge,
		"S256",
		"mcp:read",
		Principal{Provider: provider, Subject: "subject-1"},
	)
	if err != nil {
		t.Fatalf("create code: %v", err)
	}

	type result struct {
		ok  bool
		err error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, st := range []*postgresStore{storeA, storeB} {
		wg.Add(1)
		go func(st *postgresStore) {
			defer wg.Done()
			_, _, _, _, ok, err := st.exchangeCode(
				ctx, code, reg.ClientID, "http://localhost/callback", verifier,
			)
			results <- result{ok: ok, err: err}
		}(st)
	}
	wg.Wait()
	close(results)

	var successes int
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent exchange: %v", result.err)
		}
		if result.ok {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful exchanges: got %d, want 1", successes)
	}
}
