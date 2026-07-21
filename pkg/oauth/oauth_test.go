package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/crypto/bcrypt"
)

const testPassword = "test-operator-password"

func testHash(t *testing.T) []byte {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return h
}

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	srv := newTestServer(t, "")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	// Re-create with the actual test server URL as issuer.
	srv2 := newTestServer(t, ts.URL)
	ts.Config.Handler = srv2.Handler()
	return srv2, ts
}

func newTestServer(t *testing.T, issuer string) *Server {
	t.Helper()
	if issuer == "" {
		issuer = "http://localhost"
	}
	return New(issuer, testHash(t), nil)
}

func newTestServerWithLog(t *testing.T, issuer string) *Server {
	t.Helper()
	if issuer == "" {
		issuer = "http://localhost"
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(issuer, testHash(t), log)
}

func registerTestClient(t *testing.T, ts *httptest.Server) (clientID, clientSecret string) {
	t.Helper()
	body := `{"redirect_uris":["http://localhost/callback"],"client_name":"Test Client"}`
	resp, err := ts.Client().Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("register: status %d: %s", resp.StatusCode, b)
	}
	var reg oauthex.ClientRegistrationResponse
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return reg.ClientID, reg.ClientSecret
}

// pkce generates a code_verifier and code_challenge (S256).
func pkce() (verifier, challenge string) {
	verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

func TestMetadataEndpoints(t *testing.T) {
	_, ts := testServer(t)

	t.Run("protected_resource_metadata", func(t *testing.T) {
		resp, err := ts.Client().Get(ts.URL + "/.well-known/oauth-protected-resource")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: %d", resp.StatusCode)
		}
		var prm oauthex.ProtectedResourceMetadata
		if err := json.NewDecoder(resp.Body).Decode(&prm); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if prm.Resource != ts.URL {
			t.Errorf("resource: got %q, want %q", prm.Resource, ts.URL)
		}
		if len(prm.AuthorizationServers) == 0 {
			t.Error("authorization_servers empty")
		}
		if prm.AuthorizationServers[0] != ts.URL {
			t.Errorf("auth server: got %q, want %q", prm.AuthorizationServers[0], ts.URL)
		}
		// CORS header check.
		if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Error("missing CORS header on PRM")
		}
	})

	t.Run("auth_server_metadata", func(t *testing.T) {
		resp, err := ts.Client().Get(ts.URL + "/.well-known/oauth-authorization-server")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: %d", resp.StatusCode)
		}
		var asm oauthex.AuthServerMeta
		if err := json.NewDecoder(resp.Body).Decode(&asm); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if asm.Issuer != ts.URL {
			t.Errorf("issuer: got %q, want %q", asm.Issuer, ts.URL)
		}
		if asm.AuthorizationEndpoint != ts.URL+"/oauth/authorize" {
			t.Errorf("authorization_endpoint: %q", asm.AuthorizationEndpoint)
		}
		if asm.TokenEndpoint != ts.URL+"/oauth/token" {
			t.Errorf("token_endpoint: %q", asm.TokenEndpoint)
		}
		if asm.RegistrationEndpoint != ts.URL+"/oauth/register" {
			t.Errorf("registration_endpoint: %q", asm.RegistrationEndpoint)
		}
		if asm.JWKSURI != ts.URL+"/oauth/jwks" {
			t.Errorf("jwks_uri: %q", asm.JWKSURI)
		}
		// Must advertise PKCE support.
		if len(asm.CodeChallengeMethodsSupported) == 0 || asm.CodeChallengeMethodsSupported[0] != "S256" {
			t.Error("code_challenge_methods_supported must include S256")
		}
		// Must advertise CIMD support.
		if !asm.ClientIDMetadataDocumentSupported {
			t.Error("client_id_metadata_document_supported must be true")
		}
		// Must support public clients.
		found := false
		for _, m := range asm.TokenEndpointAuthMethodsSupported {
			if m == "none" {
				found = true
				break
			}
		}
		if !found {
			t.Error("token_endpoint_auth_methods_supported must include 'none'")
		}
		// CORS header check.
		if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Error("missing CORS header on ASM")
		}
	})
}

func TestJWKSEmpty(t *testing.T) {
	_, ts := testServer(t)
	resp, err := ts.Client().Get(ts.URL + "/oauth/jwks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var jwks struct {
		Keys []any `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(jwks.Keys) != 0 {
		t.Errorf("expected empty keys, got %d", len(jwks.Keys))
	}
}

func TestDynamicClientRegistration(t *testing.T) {
	_, ts := testServer(t)
	clientID, clientSecret := registerTestClient(t, ts)
	if clientID == "" {
		t.Error("empty client_id")
	}
	if clientSecret == "" {
		t.Error("empty client_secret")
	}
}

func TestDCR_MissingRedirectURIs(t *testing.T) {
	_, ts := testServer(t)
	body := `{"client_name":"Test"}`
	resp, err := ts.Client().Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestFullAuthFlow(t *testing.T) {
	srv, ts := testServer(t)
	clientID, clientSecret := registerTestClient(t, ts)
	verifier, challenge := pkce()

	// Step 1: GET /oauth/authorize — returns the login form.
	authURL := fmt.Sprintf("%s/oauth/authorize?response_type=code&client_id=%s&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&state=teststate&scope=mcp:read",
		ts.URL, url.QueryEscape(clientID), url.QueryEscape("http://localhost/callback"),
		url.QueryEscape(challenge))

	resp, err := ts.Client().Get(authURL)
	if err != nil {
		t.Fatalf("GET authorize: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorize GET: status %d", resp.StatusCode)
	}

	// Step 2: POST /oauth/authorize — submit password, get redirect with code.
	form := url.Values{
		"password":              {testPassword},
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"http://localhost/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"teststate"},
		"scope":                 {"mcp:read"},
		"resource":              {ts.URL},
	}

	// Don't follow redirects — we want the Location header.
	noRedirectClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err = noRedirectClient.PostForm(ts.URL+"/oauth/authorize", form)
	if err != nil {
		t.Fatalf("POST authorize: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize POST: status %d, want 302", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	if location == "" {
		t.Fatal("no Location header")
	}
	loc, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("no code in redirect")
	}
	if loc.Query().Get("state") != "teststate" {
		t.Errorf("state: got %q, want teststate", loc.Query().Get("state"))
	}
	// RFC 9207: iss must be present.
	if loc.Query().Get("iss") != ts.URL {
		t.Errorf("iss: got %q, want %q", loc.Query().Get("iss"), ts.URL)
	}

	// Step 3: POST /oauth/token — exchange code for tokens.
	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {"http://localhost/callback"},
		"code_verifier": {verifier},
		"resource":      {ts.URL},
	}
	resp, err = ts.Client().PostForm(ts.URL+"/oauth/token", tokenForm)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("token: status %d: %s", resp.StatusCode, b)
	}

	var tokenResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	accessToken, ok := tokenResp["access_token"].(string)
	if !ok || accessToken == "" {
		t.Fatal("no access_token")
	}
	if tokenResp["token_type"] != "Bearer" {
		t.Errorf("token_type: %v", tokenResp["token_type"])
	}
	if tokenResp["scope"] != "mcp:read" {
		t.Errorf("scope: %v", tokenResp["scope"])
	}
	if _, ok := tokenResp["refresh_token"].(string); !ok {
		t.Error("no refresh_token")
	}

	// Step 4: Verify the access token works with TokenVerifier.
	verifyFn := srv.TokenVerifier()
	info, err := verifyFn(context.Background(), accessToken, nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if info.Expiration.IsZero() {
		t.Error("expiration is zero")
	}
	if len(info.Scopes) == 0 || info.Scopes[0] != "mcp:read" {
		t.Errorf("scopes: %v", info.Scopes)
	}
}

func TestPKCERequired(t *testing.T) {
	_, ts := testServer(t)
	clientID, clientSecret := registerTestClient(t, ts)
	_, challenge := pkce()

	// Authorize to get a code.
	code := authorizeAndGetCode(t, ts, clientID, challenge)

	// Token exchange without code_verifier.
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {"http://localhost/callback"},
		// No code_verifier.
	}
	resp, err := ts.Client().PostForm(ts.URL+"/oauth/token", form)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestPKCEWrongVerifier(t *testing.T) {
	_, ts := testServer(t)
	clientID, clientSecret := registerTestClient(t, ts)
	_, challenge := pkce()

	code := authorizeAndGetCode(t, ts, clientID, challenge)

	// Token exchange with wrong code_verifier.
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {"http://localhost/callback"},
		"code_verifier": {"this-is-the-wrong-verifier-value"},
	}
	resp, err := ts.Client().PostForm(ts.URL+"/oauth/token", form)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	var errResp map[string]string
	json.NewDecoder(resp.Body).Decode(&errResp)
	if errResp["error"] != "invalid_grant" {
		t.Errorf("error: %q", errResp["error"])
	}
}

func TestTokenExpiry(t *testing.T) {
	srv := newTestServer(t, "http://localhost")
	// Directly create a token with very short TTL.
	at, _, _ := srv.store.createTokenPairWithTTL("test-client", "mcp:read", 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	verifier := srv.TokenVerifier()
	_, err := verifier(context.Background(), at, nil)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestRefreshTokenFlow(t *testing.T) {
	_, ts := testServer(t)
	clientID, clientSecret := registerTestClient(t, ts)
	verifier, challenge := pkce()

	code := authorizeAndGetCode(t, ts, clientID, challenge)

	// Exchange for tokens.
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {"http://localhost/callback"},
		"code_verifier": {verifier},
	}
	resp, err := ts.Client().PostForm(ts.URL+"/oauth/token", form)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	var tokenResp map[string]any
	json.NewDecoder(resp.Body).Decode(&tokenResp)
	rt := tokenResp["refresh_token"].(string)

	// Use refresh token.
	refreshForm := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {rt},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	resp2, err := ts.Client().PostForm(ts.URL+"/oauth/token", refreshForm)
	if err != nil {
		t.Fatalf("POST refresh: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("refresh: status %d: %s", resp2.StatusCode, b)
	}

	var refreshResp map[string]any
	json.NewDecoder(resp2.Body).Decode(&refreshResp)
	newAT := refreshResp["access_token"].(string)
	newRT := refreshResp["refresh_token"].(string)
	if newAT == "" {
		t.Error("no new access_token")
	}
	if newRT == "" {
		t.Error("no new refresh_token")
	}
	if newRT == rt {
		t.Error("refresh token not rotated")
	}

	// Old refresh token must be consumed (single-use).
	resp3, err := ts.Client().PostForm(ts.URL+"/oauth/token", refreshForm)
	if err != nil {
		t.Fatalf("POST refresh reuse: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest {
		t.Errorf("reuse of consumed refresh token: expected 400, got %d", resp3.StatusCode)
	}
}

func TestWrongPassword(t *testing.T) {
	_, ts := testServer(t)
	clientID, _ := registerTestClient(t, ts)
	_, challenge := pkce()

	form := url.Values{
		"password":              {"wrong-password"},
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"http://localhost/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"scope":                 {"mcp:read"},
	}

	resp, err := http.PostForm(ts.URL+"/oauth/authorize", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestAuthorizePost_RejectsPlainPKCEMethod(t *testing.T) {
	// Defense-in-depth: a hand-crafted POST with code_challenge_method=plain must
	// be rejected (400), not bypass the S256 requirement enforced on the GET form.
	_, ts := testServer(t)
	clientID, _ := registerTestClient(t, ts)
	_, challenge := pkce()

	form := url.Values{
		"password":              {testPassword},
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"http://localhost/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"plain"},
		"scope":                 {"mcp:read"},
	}

	noRedirect := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noRedirect.PostForm(ts.URL+"/oauth/authorize", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("plain method POST: expected 400, got %d", resp.StatusCode)
	}
}

func TestInvalidClientID(t *testing.T) {
	_, ts := testServer(t)
	_, challenge := pkce()

	authURL := fmt.Sprintf("%s/oauth/authorize?response_type=code&client_id=nonexistent&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256",
		ts.URL, url.QueryEscape("http://localhost/callback"), url.QueryEscape(challenge))

	resp, err := ts.Client().Get(authURL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestBearerFallback(t *testing.T) {
	srv, ts := testServer(t)
	clientID, clientSecret := registerTestClient(t, ts)
	verifier, challenge := pkce()

	// Get an OAuth token.
	code := authorizeAndGetCode(t, ts, clientID, challenge)
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {"http://localhost/callback"},
		"code_verifier": {verifier},
	}
	resp, _ := ts.Client().PostForm(ts.URL+"/oauth/token", form)
	var tokenResp map[string]any
	json.NewDecoder(resp.Body).Decode(&tokenResp)
	resp.Body.Close()
	oauthToken := tokenResp["access_token"].(string)

	staticToken := "my-static-bearer-token"
	fallback := srv.BearerFallback(staticToken)

	t.Run("oauth_token_accepted", func(t *testing.T) {
		info, err := fallback(context.Background(), oauthToken, nil)
		if err != nil {
			t.Fatalf("verify oauth: %v", err)
		}
		if len(info.Scopes) == 0 {
			t.Error("no scopes")
		}
	})

	t.Run("static_token_accepted", func(t *testing.T) {
		info, err := fallback(context.Background(), staticToken, nil)
		if err != nil {
			t.Fatalf("verify static: %v", err)
		}
		if len(info.Scopes) == 0 {
			t.Error("no scopes")
		}
	})

	t.Run("invalid_token_rejected", func(t *testing.T) {
		_, err := fallback(context.Background(), "bad-token", nil)
		if err == nil {
			t.Error("expected error for bad token")
		}
	})
}

func TestUnauthorizedWithoutToken(t *testing.T) {
	srv := newTestServer(t, "http://localhost")
	verifier := srv.TokenVerifier()

	t.Run("empty_token", func(t *testing.T) {
		_, err := verifier(context.Background(), "", nil)
		if err == nil {
			t.Error("expected error for empty token")
		}
	})

	t.Run("garbage_token", func(t *testing.T) {
		_, err := verifier(context.Background(), "not-a-real-token", nil)
		if err == nil {
			t.Error("expected error for garbage token")
		}
	})
}

func TestCodeReuse(t *testing.T) {
	_, ts := testServer(t)
	clientID, clientSecret := registerTestClient(t, ts)
	verifier, challenge := pkce()

	code := authorizeAndGetCode(t, ts, clientID, challenge)

	// First use succeeds.
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {"http://localhost/callback"},
		"code_verifier": {verifier},
	}
	resp, _ := ts.Client().PostForm(ts.URL+"/oauth/token", form)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first use: status %d", resp.StatusCode)
	}

	// Second use fails (codes are single-use).
	resp2, _ := ts.Client().PostForm(ts.URL+"/oauth/token", form)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("code reuse: expected 400, got %d", resp2.StatusCode)
	}
}

func TestPublicClientAuth(t *testing.T) {
	// Simulate a public client (CIMD-style) by directly registering as public.
	srv := newTestServer(t, "http://localhost")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	// Re-create with the test server URL.
	srv = newTestServer(t, ts.URL)
	ts.Config.Handler = srv.Handler()

	// Register a public client manually.
	srv.store.registerCIMDClient("https://example.com/client-meta", &oauthex.ClientRegistrationMetadata{
		RedirectURIs: []string{"http://localhost/callback"},
		ClientName:   "Test CIMD Client",
	})

	_, challenge := pkce()

	// Authorize.
	code := authorizeAndGetCodeForClient(t, ts, "https://example.com/client-meta", challenge)

	verifier, _ := pkce()

	// Token exchange without client_secret (public client).
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {"https://example.com/client-meta"},
		"redirect_uri":  {"http://localhost/callback"},
		"code_verifier": {verifier},
	}
	resp, err := ts.Client().PostForm(ts.URL+"/oauth/token", form)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("token: status %d: %s", resp.StatusCode, b)
	}
}

func TestMetadataEndpoints_Unauthenticated(t *testing.T) {
	// Verify that metadata endpoints are reachable without any auth.
	// This is an MCP spec requirement.
	_, ts := testServer(t)

	endpoints := []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-authorization-server",
		"/oauth/jwks",
	}

	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			resp, err := ts.Client().Get(ts.URL + ep)
			if err != nil {
				t.Fatalf("GET %s: %v", ep, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s: status %d, want 200", ep, resp.StatusCode)
			}
			ct := resp.Header.Get("Content-Type")
			if !strings.Contains(ct, "application/json") {
				t.Errorf("%s: Content-Type %q, want application/json", ep, ct)
			}
		})
	}
}

func TestLocalhostPortAgnosticRedirect(t *testing.T) {
	srv := newTestServer(t, "http://localhost")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	srv = newTestServer(t, ts.URL)
	ts.Config.Handler = srv.Handler()

	// Register client with just "http://localhost/callback" (no port).
	srv.store.registerCIMDClient("test-localhost-client", &oauthex.ClientRegistrationMetadata{
		RedirectURIs: []string{"http://localhost/callback"},
		ClientName:   "Localhost Test",
	})
	// Force client to be in store directly for this test.
	srv.store.mu.Lock()
	srv.store.clients["test-localhost-client"].Public = true
	srv.store.mu.Unlock()

	// Authorize with a port-specified localhost redirect (Claude Code pattern).
	authURL := fmt.Sprintf("%s/oauth/authorize?response_type=code&client_id=test-localhost-client&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256",
		ts.URL,
		url.QueryEscape("http://localhost:12345/callback"),
		url.QueryEscape("test-challenge"))

	resp, err := ts.Client().Get(authURL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	// Should succeed (port-agnostic match).
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, b)
	}
}

func TestRequireBearerTokenIntegration(t *testing.T) {
	// Verify the TokenVerifier works with the SDK's RequireBearerToken middleware.
	srv := newTestServer(t, "http://localhost")
	at, _, _ := srv.store.createTokenPair("test-client", "mcp:read")

	verifier := srv.TokenVerifier()
	mw := auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: "http://localhost/.well-known/oauth-protected-resource",
		Scopes:              []string{"mcp:read"},
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ti := auth.TokenInfoFromContext(r.Context())
		if ti == nil {
			t.Error("TokenInfo not in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("with_valid_token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+at)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("without_token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/mcp", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
		// Must include WWW-Authenticate with resource_metadata.
		wwwAuth := w.Header().Get("WWW-Authenticate")
		if !strings.Contains(wwwAuth, "resource_metadata") {
			t.Errorf("WWW-Authenticate missing resource_metadata: %q", wwwAuth)
		}
	})

	t.Run("with_invalid_token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/mcp", nil)
		req.Header.Set("Authorization", "Bearer bad-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
}

func TestClientCapRejection(t *testing.T) {
	_, ts := testServer(t)

	// Register clients up to the cap.
	for i := 0; i < maxClients; i++ {
		body := fmt.Sprintf(`{"redirect_uris":["http://localhost/cb"],"client_name":"client-%d"}`, i)
		resp, err := ts.Client().Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("register %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("register %d: status %d, want 201", i, resp.StatusCode)
		}
	}

	// One more should fail.
	body := `{"redirect_uris":["http://localhost/cb"],"client_name":"overflow"}`
	resp, err := ts.Client().Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("register overflow: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("overflow: status %d, want 503", resp.StatusCode)
	}
	var errResp map[string]string
	json.NewDecoder(resp.Body).Decode(&errResp)
	if errResp["error"] != "temporarily_unavailable" {
		t.Errorf("error code: %q, want temporarily_unavailable", errResp["error"])
	}
}

func TestIdleClientEviction(t *testing.T) {
	srv := newTestServer(t, "http://localhost")

	// Manually register two clients: one active, one idle.
	srv.store.mu.Lock()
	stale := time.Now().Add(-25 * time.Hour)
	srv.store.clients["idle"] = &client{
		ID:           "idle",
		RedirectURIs: []string{"http://localhost/cb"},
		CreatedAt:    stale,
		LastActivity: stale,
	}
	srv.store.clients["active"] = &client{
		ID:           "active",
		RedirectURIs: []string{"http://localhost/cb"},
		CreatedAt:    stale,
		LastActivity: time.Now(),
	}
	srv.store.clients["authorized"] = &client{
		ID:           "authorized",
		RedirectURIs: []string{"http://localhost/cb"},
		CreatedAt:    stale,
		LastActivity: stale,
		Authorized:   true,
	}
	srv.store.mu.Unlock()

	srv.store.evict()

	if srv.store.lookupClient("idle") != nil {
		t.Error("idle client should have been evicted")
	}
	if srv.store.lookupClient("active") == nil {
		t.Error("active client should survive eviction")
	}
	if srv.store.lookupClient("authorized") == nil {
		t.Error("authorized client must never be evicted")
	}
}

func TestInvalidScopeRejection(t *testing.T) {
	_, ts := testServer(t)
	clientID, _ := registerTestClient(t, ts)
	_, challenge := pkce()

	t.Run("authorize_get_rejects_bad_scope", func(t *testing.T) {
		authURL := fmt.Sprintf("%s/oauth/authorize?response_type=code&client_id=%s&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&scope=admin:write",
			ts.URL, url.QueryEscape(clientID), url.QueryEscape("http://localhost/callback"), url.QueryEscape(challenge))
		resp, err := ts.Client().Get(authURL)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("bad scope GET: status %d, want 400", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "invalid_scope") {
			t.Errorf("response should mention invalid_scope, got: %s", body)
		}
	})

	t.Run("authorize_post_rejects_bad_scope", func(t *testing.T) {
		form := url.Values{
			"password":              {testPassword},
			"response_type":         {"code"},
			"client_id":             {clientID},
			"redirect_uri":          {"http://localhost/callback"},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"scope":                 {"openid profile"},
		}
		noRedirect := &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := noRedirect.PostForm(ts.URL+"/oauth/authorize", form)
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("bad scope POST: status %d, want 400", resp.StatusCode)
		}
	})

	t.Run("valid_scope_accepted", func(t *testing.T) {
		authURL := fmt.Sprintf("%s/oauth/authorize?response_type=code&client_id=%s&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&scope=mcp:read",
			ts.URL, url.QueryEscape(clientID), url.QueryEscape("http://localhost/callback"), url.QueryEscape(challenge))
		resp, err := ts.Client().Get(authURL)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("valid scope: status %d, want 200", resp.StatusCode)
		}
	})
}

func TestCIMDFetchRateLimit(t *testing.T) {
	srv := newTestServerWithLog(t, "http://localhost")

	// Drain the CIMD limiter's burst capacity (5 tokens).
	for i := 0; i < 5; i++ {
		srv.cimdLimiter.Allow()
	}

	// Next resolveClient for an HTTPS client_id should be rate-limited and
	// return nil without making a fetch.
	c := srv.resolveClient("https://attacker.example.com/meta")
	if c != nil {
		t.Error("expected nil from rate-limited CIMD resolve")
	}
}

// --- helpers -----------------------------------------------------------------

// authorizeAndGetCode runs the authorize flow and returns the auth code.
func authorizeAndGetCode(t *testing.T, ts *httptest.Server, clientID, challenge string) string {
	t.Helper()
	return authorizeAndGetCodeForClient(t, ts, clientID, challenge)
}

func authorizeAndGetCodeForClient(t *testing.T, ts *httptest.Server, clientID, challenge string) string {
	t.Helper()

	form := url.Values{
		"password":              {testPassword},
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"http://localhost/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"scope":                 {"mcp:read"},
	}

	noRedirect := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noRedirect.PostForm(ts.URL+"/oauth/authorize", form)
	if err != nil {
		t.Fatalf("POST authorize: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize: status %d, want 302", resp.StatusCode)
	}

	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("no code in redirect")
	}
	return code
}
