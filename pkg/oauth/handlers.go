package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/crypto/bcrypt"
)

// authServerMetadataHandler serves RFC 8414 authorization server metadata.
func (s *Server) authServerMetadataHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Cache-Control", "no-store")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	meta := &oauthex.AuthServerMeta{
		Issuer:                            s.issuer,
		AuthorizationEndpoint:             s.issuer + "/oauth/authorize",
		TokenEndpoint:                     s.issuer + "/oauth/token",
		RegistrationEndpoint:              s.issuer + "/oauth/register",
		JWKSURI:                           s.issuer + "/oauth/jwks",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_post"},
		ScopesSupported:                   []string{"mcp:read"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		ServiceDocumentation:              "https://github.com/dannyota/compliary",
		ClientIDMetadataDocumentSupported: true,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(meta); err != nil {
		s.log.Error("failed to encode auth server metadata", slog.String("error", err.Error()))
	}
}

// jwksHandler serves an empty JWKS document (opaque tokens, no signing keys).
func (s *Server) jwksHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"keys":[]}`))
}

// registerHandler handles RFC 7591 dynamic client registration.
func (s *Server) registerHandler(w http.ResponseWriter, r *http.Request) {
	var meta oauthex.ClientRegistrationMetadata
	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "malformed request body")
		return
	}

	if len(meta.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "redirect_uris is required")
		return
	}

	resp, err := s.store.registerClient(r.Context(), meta)
	if err != nil {
		if errors.Is(err, errClientCapReached) {
			writeOAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "too many registered clients")
			return
		}
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "registration failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.log.Error("failed to encode registration response", slog.String("error", err.Error()))
	}
}

// authorizeHandler serves the login/consent form (GET) and processes it (POST).
func (s *Server) authorizeHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.authorizeGet(w, r)
	case http.MethodPost:
		s.authorizePost(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// resolveClient looks up a client by ID from the store. If clientID is an HTTPS
// URL (CIMD per MCP spec), it fetches the client metadata document from that URL,
// validates it, and auto-registers the client as a public client (no secret).
func (s *Server) resolveClient(ctx context.Context, clientID string) (*client, error) {
	// Check store first.
	c, err := s.store.lookupClient(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if c != nil && clientMetadataFresh(c, clientID, time.Now()) {
		return c, nil
	}

	// CIMD: if client_id is an HTTPS URL, fetch the metadata document.
	if !strings.HasPrefix(clientID, "https://") {
		return nil, nil
	}

	// Rate-limit outbound CIMD fetches to prevent request amplification.
	if !s.cimdLimiter.Allow() {
		s.log.Warn("CIMD fetch rate-limited", slog.String("client_id", clientID))
		return nil, nil
	}

	meta, err := fetchClientMetadata(clientID)
	if err != nil {
		s.log.Warn("CIMD fetch failed", slog.String("client_id", clientID), slog.String("error", err.Error()))
		return nil, nil
	}

	// Register as a public client (no secret).
	if err := s.store.registerCIMDClient(ctx, clientID, meta); err != nil {
		s.log.Warn("CIMD client registration failed", slog.String("client_id", clientID), slog.String("error", err.Error()))
		if errors.Is(err, errClientCapReached) {
			return nil, nil
		}
		return nil, err
	}
	return s.store.lookupClient(ctx, clientID)
}

func clientMetadataFresh(c *client, clientID string, now time.Time) bool {
	if !c.Public || !strings.HasPrefix(clientID, "https://") {
		return true
	}
	return c.MetadataRefreshedAt != nil &&
		now.Before(c.MetadataRefreshedAt.Add(cimdMetadataTTL))
}

// fetchClientMetadata is in safefetch.go (SSRF-guarded).

func (s *Server) authorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	responseType := q.Get("response_type")
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	codeChallenge := q.Get("code_challenge")
	challengeMethod := q.Get("code_challenge_method")
	state := q.Get("state")
	scope := q.Get("scope")
	resource := q.Get("resource")

	if responseType != "code" {
		http.Error(w, "unsupported_response_type: must be 'code'", http.StatusBadRequest)
		return
	}

	if challengeMethod != "S256" {
		http.Error(w, "invalid_request: code_challenge_method must be S256", http.StatusBadRequest)
		return
	}

	if codeChallenge == "" {
		http.Error(w, "invalid_request: code_challenge is required", http.StatusBadRequest)
		return
	}

	c, err := s.resolveClient(r.Context(), clientID)
	if err != nil {
		http.Error(w, "server_error: client lookup failed", http.StatusInternalServerError)
		return
	}
	if c == nil {
		http.Error(w, "invalid_request: unknown client_id", http.StatusBadRequest)
		return
	}

	if !matchRedirectURI(c.RedirectURIs, redirectURI) {
		http.Error(w, "invalid_request: redirect_uri not registered", http.StatusBadRequest)
		return
	}

	if scope == "" {
		scope = "mcp:read"
	}
	if !validScope(scope) {
		http.Error(w, "invalid_scope: unsupported scope requested", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, loginFormHTML,
		html.EscapeString(responseType),
		html.EscapeString(clientID),
		html.EscapeString(redirectURI),
		html.EscapeString(codeChallenge),
		html.EscapeString(challengeMethod),
		html.EscapeString(state),
		html.EscapeString(scope),
		html.EscapeString(c.Name),
		html.EscapeString(resource),
	)
}

func (s *Server) authorizePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid_request: malformed form", http.StatusBadRequest)
		return
	}

	password := r.PostFormValue("password")
	clientID := r.PostFormValue("client_id")
	redirectURI := r.PostFormValue("redirect_uri")
	codeChallenge := r.PostFormValue("code_challenge")
	challengeMethod := r.PostFormValue("code_challenge_method")
	state := r.PostFormValue("state")
	scope := r.PostFormValue("scope")

	// Defense-in-depth: re-validate PKCE method on POST (the GET form path also
	// checks it, but a hand-crafted POST must not bypass the S256 requirement).
	if challengeMethod != "S256" {
		http.Error(w, "invalid_request: code_challenge_method must be S256", http.StatusBadRequest)
		return
	}
	if codeChallenge == "" {
		http.Error(w, "invalid_request: code_challenge is required", http.StatusBadRequest)
		return
	}

	// Verify operator password.
	if err := bcrypt.CompareHashAndPassword(s.operatorHash, []byte(password)); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, errorPageHTML)
		return
	}

	// The current password login represents one stable local identity. The
	// authorizer is deliberately allow-all today; future OIDC login can supply
	// a verified email without changing token persistence.
	principal := Principal{Provider: "local", Subject: "operator"}
	if err := s.authorizer.Authorize(r.Context(), principal); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, errorPageHTML)
		return
	}

	// Verify client.
	c, err := s.resolveClient(r.Context(), clientID)
	if err != nil {
		http.Error(w, "server_error: client lookup failed", http.StatusInternalServerError)
		return
	}
	if c == nil {
		http.Error(w, "invalid_request: unknown client_id", http.StatusBadRequest)
		return
	}
	if !matchRedirectURI(c.RedirectURIs, redirectURI) {
		http.Error(w, "invalid_request: redirect_uri not registered", http.StatusBadRequest)
		return
	}

	if scope == "" {
		scope = "mcp:read"
	}
	if !validScope(scope) {
		http.Error(w, "invalid_scope: unsupported scope requested", http.StatusBadRequest)
		return
	}

	code, err := s.store.createCode(
		r.Context(), clientID, redirectURI, codeChallenge, challengeMethod, scope, principal,
	)
	if err != nil {
		http.Error(w, "server_error: authorization could not be saved", http.StatusInternalServerError)
		return
	}

	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid_request: bad redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	// RFC 9207: include issuer in authorization response.
	q.Set("iss", s.issuer)
	u.RawQuery = q.Encode()

	http.Redirect(w, r, u.String(), http.StatusFound)
}

// matchRedirectURI checks if the presented redirect_uri matches any registered
// URI. For localhost URIs (Claude Code), port is ignored per MCP spec.
func matchRedirectURI(registered []string, presented string) bool {
	if slices.Contains(registered, presented) {
		return true
	}

	// Port-agnostic matching for localhost/127.0.0.1 (Claude Code uses varying ports).
	pu, err := url.Parse(presented)
	if err != nil {
		return false
	}
	presentedHost := pu.Hostname()
	if presentedHost != "localhost" && presentedHost != "127.0.0.1" {
		return false
	}

	for _, reg := range registered {
		ru, err := url.Parse(reg)
		if err != nil {
			continue
		}
		regHost := ru.Hostname()
		if (regHost == "localhost" || regHost == "127.0.0.1") &&
			ru.Scheme == pu.Scheme && ru.Path == pu.Path {
			return true
		}
	}
	return false
}

// tokenHandler handles the token endpoint (authorization_code and refresh_token grants).
func (s *Server) tokenHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}

	grantType := r.PostFormValue("grant_type")

	switch grantType {
	case "authorization_code":
		s.handleAuthCodeGrant(w, r)
	case "refresh_token":
		s.handleRefreshGrant(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

// authenticateClient validates client credentials at the token endpoint.
// Supports both client_secret_post (DCR clients) and "none" (public/CIMD clients).
func (s *Server) authenticateClient(r *http.Request) (*client, string, error) {
	clientID := r.PostFormValue("client_id")
	clientSecret := r.PostFormValue("client_secret")

	c, err := s.store.lookupClient(r.Context(), clientID)
	if err != nil {
		return nil, "", err
	}
	if c == nil {
		return nil, "unknown client_id", nil
	}

	// Public client (CIMD): no secret required.
	if c.Public {
		return c, "", nil
	}

	// Confidential client (DCR): verify secret.
	if subtle.ConstantTimeCompare(c.SecretHash, hashToken(clientSecret)) != 1 {
		return nil, "bad client_secret", nil
	}
	return c, "", nil
}

func (s *Server) handleAuthCodeGrant(w http.ResponseWriter, r *http.Request) {
	c, errMsg, err := s.authenticateClient(r)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "client lookup failed")
		return
	}
	if c == nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", errMsg)
		return
	}

	code := r.PostFormValue("code")
	redirectURI := r.PostFormValue("redirect_uri")
	codeVerifier := r.PostFormValue("code_verifier")

	// PKCE verification: RFC 7636 requires code_verifier to be 43-128 characters.
	if codeVerifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier is required")
		return
	}
	if len(codeVerifier) < 43 || len(codeVerifier) > 128 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier must be 43-128 characters")
		return
	}
	// Consume the code and create its first token pair atomically. The
	// PostgreSQL store locks the code row, so two server instances cannot both
	// exchange it and a crash cannot land between consumption and token minting.
	scope, at, rt, expiresIn, ok, err := s.store.exchangeCode(
		r.Context(), code, c.ID, redirectURI, codeVerifier,
	)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "authorization code exchange failed")
		return
	}
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid or expired authorization code")
		return
	}
	writeTokenResponse(w, at, rt, expiresIn, scope)
}

func (s *Server) handleRefreshGrant(w http.ResponseWriter, r *http.Request) {
	c, errMsg, err := s.authenticateClient(r)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "client lookup failed")
		return
	}
	if c == nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", errMsg)
		return
	}

	refreshToken := r.PostFormValue("refresh_token")

	// Atomic consume + mint: the family inherits across rotations, replay of
	// any ancestor revokes the whole family, and no lock gap exists for a
	// replay to race a legitimate rotation.
	scope, at, rt, expiresIn, ok, err := s.store.rotateRefresh(
		r.Context(), refreshToken, c.ID, s.authorizer,
	)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "refresh failed")
		return
	}
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid or expired refresh token")
		return
	}
	writeTokenResponse(w, at, rt, expiresIn, scope)
}

// verifyPKCE verifies a PKCE S256 code challenge.
func verifyPKCE(codeVerifier, codeChallenge string) bool {
	h := sha256.Sum256([]byte(codeVerifier))
	computed := base64.RawURLEncoding.EncodeToString(h[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(codeChallenge)) == 1
}

func writeTokenResponse(w http.ResponseWriter, accessToken, refreshToken string, expiresIn int, scope string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token":  accessToken,
		"token_type":    "Bearer",
		"expires_in":    expiresIn,
		"refresh_token": refreshToken,
		"scope":         scope,
	})
}

// supportedScopes is the set of scopes this server accepts.
var supportedScopes = map[string]bool{
	"mcp:read": true,
}

// validScope returns true if every space-delimited scope token is supported.
func validScope(scope string) bool {
	for _, s := range strings.Fields(scope) {
		if !supportedScopes[s] {
			return false
		}
	}
	return true
}

func writeOAuthError(w http.ResponseWriter, status int, errCode, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error":             errCode,
		"error_description": description,
	})
}

var loginFormHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>compliary — Authorize</title>
<style>
body{font-family:system-ui,sans-serif;max-width:400px;margin:80px auto;padding:0 16px}
h1{font-size:1.25rem}
label{display:block;margin:12px 0 4px}
input[type=password]{width:100%%;padding:8px;box-sizing:border-box}
button{margin-top:16px;padding:8px 24px}
.client{color:#666;font-size:0.9rem}
</style></head><body>
<h1>compliary — Authorize</h1>
<p class="client">Client: %[8]s</p>
<form method="POST" action="/oauth/authorize">
<input type="hidden" name="response_type" value="%[1]s">
<input type="hidden" name="client_id" value="%[2]s">
<input type="hidden" name="redirect_uri" value="%[3]s">
<input type="hidden" name="code_challenge" value="%[4]s">
<input type="hidden" name="code_challenge_method" value="%[5]s">
<input type="hidden" name="state" value="%[6]s">
<input type="hidden" name="scope" value="%[7]s">
<input type="hidden" name="resource" value="%[9]s">
<label for="password">Operator password</label>
<input type="password" id="password" name="password" required autofocus>
<button type="submit">Authorize</button>
</form></body></html>`

var errorPageHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>compliary — Access Denied</title>
<style>body{font-family:system-ui,sans-serif;max-width:400px;margin:80px auto;padding:0 16px}</style>
</head><body><h1>Access Denied</h1><p>The login could not be authorized.</p></body></html>`
