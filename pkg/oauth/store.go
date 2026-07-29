// Package oauth implements an OAuth 2.0 authorization server for compliary's
// MCP endpoint.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	// tokenIdleTTL is a sliding inactivity timeout. Successful access-token
	// verification renews both that access token and its live refresh token.
	tokenIdleTTL    = 30 * 24 * time.Hour
	accessTokenTTL  = tokenIdleTTL
	refreshTokenTTL = tokenIdleTTL
	authCodeTTL     = 10 * time.Minute

	// maxClients caps the number of registered clients (DCR + CIMD combined).
	// Prevents memory exhaustion from unauthenticated POST /oauth/register.
	maxClients = 50

	// clientIdleTTL is how long a client can sit without any token/code
	// activity before eviction. Statically configured clients (authorized)
	// are never evicted.
	clientIdleTTL = 24 * time.Hour

	// cimdMetadataTTL bounds how long redirect metadata fetched from a public
	// client_id URL is trusted before a new authorization must refresh it.
	cimdMetadataTTL = 24 * time.Hour
)

type store struct {
	mu       sync.Mutex
	clients  map[string]*client       // client_id -> client
	codes    map[string]*authCode     // code -> authCode
	tokens   map[string]*tokenEntry   // access_token -> tokenEntry
	refresh  map[string]*refreshEntry // refresh_token -> refreshEntry
	consumed map[string]string        // consumed refresh_token -> familyID (for replay detection)
}

func newStore() *store {
	return &store{
		clients:  make(map[string]*client),
		codes:    make(map[string]*authCode),
		tokens:   make(map[string]*tokenEntry),
		refresh:  make(map[string]*refreshEntry),
		consumed: make(map[string]string),
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func (s *store) registerClient(_ context.Context, meta oauthex.ClientRegistrationMetadata) (*oauthex.ClientRegistrationResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.clients) >= maxClients {
		return nil, errClientCapReached
	}

	id := randomHex(16)
	secret := randomHex(32)
	now := time.Now()

	c := &client{
		ID:           id,
		SecretHash:   hashToken(secret),
		RedirectURIs: meta.RedirectURIs,
		Name:         meta.ClientName,
		CreatedAt:    now,
	}
	s.clients[id] = c

	return &oauthex.ClientRegistrationResponse{
		ClientRegistrationMetadata: meta,
		ClientID:                   id,
		ClientSecret:               secret,
		ClientIDIssuedAt:           now,
	}, nil
}

func (s *store) lookupClient(_ context.Context, clientID string) (*client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.clients[clientID]
	if c == nil {
		return nil, nil
	}
	result := *c
	result.SecretHash = append([]byte(nil), c.SecretHash...)
	result.RedirectURIs = append([]string(nil), c.RedirectURIs...)
	return &result, nil
}

// registerCIMDClient registers a public client from a Client ID Metadata Document.
// The clientID is the HTTPS URL of the metadata document itself.
func (s *store) registerCIMDClient(_ context.Context, clientID string, meta *oauthex.ClientRegistrationMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Allow re-registration of an existing client (metadata refresh).
	if _, exists := s.clients[clientID]; !exists && len(s.clients) >= maxClients {
		return errClientCapReached
	}

	now := time.Now()
	s.clients[clientID] = &client{
		ID:                  clientID,
		RedirectURIs:        meta.RedirectURIs,
		Name:                meta.ClientName,
		Public:              true,
		CreatedAt:           now,
		MetadataRefreshedAt: &now,
	}
	return nil
}

func (s *store) createCode(_ context.Context, clientID, redirectURI, codeChallenge, challengeMethod, scope string, principal Principal) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	code := randomHex(32)
	now := time.Now()
	s.codes[code] = &authCode{
		Code:            code,
		ClientID:        clientID,
		RedirectURI:     redirectURI,
		CodeChallenge:   codeChallenge,
		ChallengeMethod: challengeMethod,
		Scope:           scope,
		ExpiresAt:       now.Add(authCodeTTL),
		Principal:       principal,
	}
	// Track activity for idle-client eviction.
	if c := s.clients[clientID]; c != nil {
		c.LastActivity = now
	}
	return code, nil
}

func (s *store) consumeCode(_ context.Context, code string) (*authCode, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ac, ok := s.codes[code]
	if !ok {
		return nil, false, nil
	}
	delete(s.codes, code)
	if time.Now().After(ac.ExpiresAt) {
		return nil, false, nil
	}
	result := *ac
	return &result, true, nil
}

func (s *store) exchangeCode(_ context.Context, code, clientID, redirectURI, codeVerifier string) (scope, accessToken, refreshToken string, expiresIn int, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ac, found := s.codes[code]
	if !found {
		return "", "", "", 0, false, nil
	}
	// An authorization code is single-use even when the exchange request is
	// invalid, matching the former consume-then-validate behavior.
	delete(s.codes, code)
	if time.Now().After(ac.ExpiresAt) ||
		ac.ClientID != clientID ||
		ac.RedirectURI != redirectURI ||
		!verifyPKCE(codeVerifier, ac.CodeChallenge) {
		return "", "", "", 0, false, nil
	}

	at, rt, ttl := s.createTokenPairLocked(clientID, ac.Scope, "", ac.Principal, accessTokenTTL)
	return ac.Scope, at, rt, ttl, true, nil
}

func (s *store) createTokenPair(_ context.Context, clientID, scope, familyID string, principal Principal) (accessToken, refreshToken string, expiresIn int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	accessToken, refreshToken, expiresIn = s.createTokenPairLocked(
		clientID, scope, familyID, principal, accessTokenTTL,
	)
	return accessToken, refreshToken, expiresIn, nil
}

func (s *store) createTokenPairLocked(clientID, scope, familyID string, principal Principal, atTTL time.Duration) (accessToken, refreshToken string, expiresIn int) {
	if familyID == "" {
		familyID = randomHex(16)
	}

	at := randomHex(32)
	rt := randomHex(32)
	now := time.Now()

	s.tokens[at] = &tokenEntry{
		Token:     at,
		ClientID:  clientID,
		Scope:     scope,
		ExpiresAt: now.Add(atTTL),
		FamilyID:  familyID,
		Principal: principal,
	}
	s.refresh[rt] = &refreshEntry{
		Token:     rt,
		ClientID:  clientID,
		Scope:     scope,
		ExpiresAt: now.Add(refreshTokenTTL),
		FamilyID:  familyID,
		Principal: principal,
	}
	// Track activity for idle-client eviction.
	if c := s.clients[clientID]; c != nil {
		c.LastActivity = now
	}
	return at, rt, int(atTTL.Seconds())
}

// createTokenPairWithTTL is like createTokenPair but with a custom access token TTL.
// Used for testing token expiry.
func (s *store) createTokenPairWithTTL(clientID, scope string, atTTL time.Duration) (accessToken, refreshToken string, expiresIn int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	familyID := randomHex(16)
	at := randomHex(32)
	rt := randomHex(32)
	now := time.Now()

	s.tokens[at] = &tokenEntry{
		Token:     at,
		ClientID:  clientID,
		Scope:     scope,
		ExpiresAt: now.Add(atTTL),
		FamilyID:  familyID,
	}
	s.refresh[rt] = &refreshEntry{
		Token:     rt,
		ClientID:  clientID,
		Scope:     scope,
		ExpiresAt: now.Add(refreshTokenTTL),
		FamilyID:  familyID,
	}
	if c := s.clients[clientID]; c != nil {
		c.LastActivity = now
	}
	return at, rt, int(atTTL.Seconds())
}

func (s *store) lookupToken(_ context.Context, accessToken string) (*tokenEntry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	te, ok := s.tokens[accessToken]
	if !ok {
		return nil, false, nil
	}
	now := time.Now()
	if now.After(te.ExpiresAt) {
		delete(s.tokens, accessToken)
		return nil, false, nil
	}

	result := *te
	return &result, true, nil
}

func (s *store) renewToken(_ context.Context, accessToken string) (*tokenEntry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	te, ok := s.tokens[accessToken]
	if !ok {
		return nil, false, nil
	}
	now := time.Now()
	if now.After(te.ExpiresAt) {
		delete(s.tokens, accessToken)
		return nil, false, nil
	}

	// Keep active sessions alive for tokenIdleTTL. OAuth clients only know the
	// expires_in value returned when the pair was issued, so renew the live
	// refresh token too; otherwise a client refreshing on its original schedule
	// would present a refresh token that expired despite regular access-token use.
	expiresAt := now.Add(tokenIdleTTL)
	te.ExpiresAt = expiresAt
	for token, re := range s.refresh {
		if re.FamilyID != te.FamilyID {
			continue
		}
		if now.After(re.ExpiresAt) {
			delete(s.refresh, token)
			continue
		}
		re.ExpiresAt = expiresAt
	}
	if c := s.clients[te.ClientID]; c != nil {
		c.LastActivity = now
	}

	// Return a snapshot so concurrent token uses cannot race with the caller
	// reading the renewed expiry.
	result := *te
	return &result, true, nil
}

// rotateRefresh atomically consumes a refresh token and mints its successor
// pair inside one critical section. Consume and create must not be separate
// lock acquisitions: a replayed token racing a legitimate rotation could
// otherwise revoke the family between the two steps and still let the replay
// mint a live pair afterwards. If the token was already consumed (replay),
// the whole family is revoked per OAuth 2.1 §6.1 and ok is false. The client
// check happens before consumption so a wrong client cannot burn the token.
func (s *store) rotateRefresh(ctx context.Context, refreshToken, clientID string, authorizer Authorizer) (scope, accessToken, newRefresh string, expiresIn int, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	re, found := s.refresh[refreshToken]
	if !found {
		// Replayed (already-consumed) token → kill the family.
		if familyID, consumed := s.consumed[refreshToken]; consumed {
			s.revokeFamilyLocked(familyID)
		}
		return "", "", "", 0, false, nil
	}
	if re.ClientID != clientID {
		return "", "", "", 0, false, nil
	}
	if time.Now().After(re.ExpiresAt) {
		delete(s.refresh, refreshToken)
		return "", "", "", 0, false, nil
	}
	if authorizer != nil {
		if err := authorizer.Authorize(ctx, re.Principal); err != nil {
			return "", "", "", 0, false, nil
		}
	}
	delete(s.refresh, refreshToken)
	if time.Now().After(re.ExpiresAt) {
		return "", "", "", 0, false, nil
	}
	// Record the consumed token so replays trigger family revocation.
	s.consumed[refreshToken] = re.FamilyID

	at := randomHex(32)
	rt := randomHex(32)
	now := time.Now()
	s.tokens[at] = &tokenEntry{
		Token:     at,
		ClientID:  clientID,
		Scope:     re.Scope,
		ExpiresAt: now.Add(accessTokenTTL),
		FamilyID:  re.FamilyID,
		Principal: re.Principal,
	}
	s.refresh[rt] = &refreshEntry{
		Token:     rt,
		ClientID:  clientID,
		Scope:     re.Scope,
		ExpiresAt: now.Add(refreshTokenTTL),
		FamilyID:  re.FamilyID,
		Principal: re.Principal,
	}
	if c := s.clients[clientID]; c != nil {
		c.LastActivity = now
	}
	return re.Scope, at, rt, int(accessTokenTTL.Seconds()), true, nil
}

// revokeFamilyLocked revokes all access tokens, refresh tokens, and consumed-
// token records belonging to the given family. Caller must hold s.mu.
func (s *store) revokeFamilyLocked(familyID string) {
	for k, v := range s.tokens {
		if v.FamilyID == familyID {
			delete(s.tokens, k)
		}
	}
	for k, v := range s.refresh {
		if v.FamilyID == familyID {
			delete(s.refresh, k)
		}
	}
	for k, fid := range s.consumed {
		if fid == familyID {
			delete(s.consumed, k)
		}
	}
}

// startEvictor runs a periodic cleanup of expired entries. Returns a stop function.
func (s *store) evict(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for k, v := range s.codes {
		if now.After(v.ExpiresAt) {
			delete(s.codes, k)
		}
	}
	for k, v := range s.tokens {
		if now.After(v.ExpiresAt) {
			delete(s.tokens, k)
		}
	}
	for k, v := range s.refresh {
		if now.After(v.ExpiresAt) {
			delete(s.refresh, k)
		}
	}
	// Consumed-token replay records: evict entries whose family has no live
	// refresh tokens left (the family is fully expired or revoked).
	liveFamily := make(map[string]bool, len(s.refresh))
	for _, v := range s.refresh {
		liveFamily[v.FamilyID] = true
	}
	for k, fid := range s.consumed {
		if !liveFamily[fid] {
			delete(s.consumed, k)
		}
	}
	// A registered client with a live grant must remain available for refresh.
	// This also means the 24h registration cap cleanup still applies to unused
	// registrations without cutting off a valid 30-day session.
	clientsWithLiveGrants := make(map[string]bool)
	for _, v := range s.codes {
		clientsWithLiveGrants[v.ClientID] = true
	}
	for _, v := range s.tokens {
		clientsWithLiveGrants[v.ClientID] = true
	}
	for _, v := range s.refresh {
		clientsWithLiveGrants[v.ClientID] = true
	}

	// Evict idle clients with no live grants. Authorized (statically configured)
	// clients are never evicted.
	cutoff := now.Add(-clientIdleTTL)
	for k, c := range s.clients {
		if c.Authorized || clientsWithLiveGrants[k] {
			continue
		}
		activity := c.LastActivity
		if activity.IsZero() {
			activity = c.CreatedAt
		}
		if activity.Before(cutoff) {
			delete(s.clients, k)
		}
	}
	return nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
