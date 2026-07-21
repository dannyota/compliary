// Package oauth implements an OAuth 2.0 authorization server for compliary's
// single-user MCP endpoint. It uses opaque tokens with in-memory storage —
// no JWT signing, no JWKS keys, no external token introspection.
package oauth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	accessTokenTTL  = 1 * time.Hour
	refreshTokenTTL = 7 * 24 * time.Hour
	authCodeTTL     = 10 * time.Minute

	// maxClients caps the number of registered clients (DCR + CIMD combined).
	// Prevents memory exhaustion from unauthenticated POST /oauth/register.
	maxClients = 50

	// clientIdleTTL is how long a client can sit without any token/code
	// activity before eviction. Statically configured clients (authorized)
	// are never evicted.
	clientIdleTTL = 24 * time.Hour
)

type store struct {
	mu       sync.Mutex
	clients  map[string]*client       // client_id -> client
	codes    map[string]*authCode     // code -> authCode
	tokens   map[string]*tokenEntry   // access_token -> tokenEntry
	refresh  map[string]*refreshEntry // refresh_token -> refreshEntry
	consumed map[string]string        // consumed refresh_token -> familyID (for replay detection)
}

type client struct {
	ID           string
	Secret       string
	RedirectURIs []string
	Name         string
	Public       bool // true for CIMD clients (no secret)
	CreatedAt    time.Time
	LastActivity time.Time // updated on code/token creation; zero = never used
	Authorized   bool      // statically configured — exempt from eviction
}

type authCode struct {
	Code            string
	ClientID        string
	RedirectURI     string
	CodeChallenge   string
	ChallengeMethod string
	Scope           string
	ExpiresAt       time.Time
}

type tokenEntry struct {
	Token     string
	ClientID  string
	Scope     string
	ExpiresAt time.Time
	FamilyID  string // token family for refresh-reuse revocation
}

type refreshEntry struct {
	Token     string
	ClientID  string
	Scope     string
	ExpiresAt time.Time
	FamilyID  string // inherited from the originating auth code exchange
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

// errClientCapReached is returned when the client registration cap is hit.
var errClientCapReached = fmt.Errorf("client registration cap reached")

func (s *store) registerClient(meta oauthex.ClientRegistrationMetadata) (*oauthex.ClientRegistrationResponse, error) {
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
		Secret:       secret,
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

func (s *store) lookupClient(clientID string) *client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clients[clientID]
}

// registerCIMDClient registers a public client from a Client ID Metadata Document.
// The clientID is the HTTPS URL of the metadata document itself.
func (s *store) registerCIMDClient(clientID string, meta *oauthex.ClientRegistrationMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Allow re-registration of an existing client (metadata refresh).
	if _, exists := s.clients[clientID]; !exists && len(s.clients) >= maxClients {
		return errClientCapReached
	}

	s.clients[clientID] = &client{
		ID:           clientID,
		RedirectURIs: meta.RedirectURIs,
		Name:         meta.ClientName,
		Public:       true,
		CreatedAt:    time.Now(),
	}
	return nil
}

func (s *store) createCode(clientID, redirectURI, codeChallenge, challengeMethod, scope string) string {
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
	}
	// Track activity for idle-client eviction.
	if c := s.clients[clientID]; c != nil {
		c.LastActivity = now
	}
	return code
}

func (s *store) consumeCode(code string) (*authCode, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ac, ok := s.codes[code]
	if !ok {
		return nil, false
	}
	delete(s.codes, code)
	if time.Now().After(ac.ExpiresAt) {
		return nil, false
	}
	return ac, true
}

func (s *store) createTokenPair(clientID, scope, familyID string) (accessToken, refreshToken string, expiresIn int) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
		ExpiresAt: now.Add(accessTokenTTL),
		FamilyID:  familyID,
	}
	s.refresh[rt] = &refreshEntry{
		Token:     rt,
		ClientID:  clientID,
		Scope:     scope,
		ExpiresAt: now.Add(refreshTokenTTL),
		FamilyID:  familyID,
	}
	// Track activity for idle-client eviction.
	if c := s.clients[clientID]; c != nil {
		c.LastActivity = now
	}
	return at, rt, int(accessTokenTTL.Seconds())
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

func (s *store) lookupToken(accessToken string) (*tokenEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	te, ok := s.tokens[accessToken]
	if !ok {
		return nil, false
	}
	if time.Now().After(te.ExpiresAt) {
		delete(s.tokens, accessToken)
		return nil, false
	}
	return te, true
}

// rotateRefresh atomically consumes a refresh token and mints its successor
// pair inside one critical section. Consume and create must not be separate
// lock acquisitions: a replayed token racing a legitimate rotation could
// otherwise revoke the family between the two steps and still let the replay
// mint a live pair afterwards. If the token was already consumed (replay),
// the whole family is revoked per OAuth 2.1 §6.1 and ok is false. The client
// check happens before consumption so a wrong client cannot burn the token.
func (s *store) rotateRefresh(refreshToken, clientID string) (scope, accessToken, newRefresh string, expiresIn int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	re, found := s.refresh[refreshToken]
	if !found {
		// Replayed (already-consumed) token → kill the family.
		if familyID, consumed := s.consumed[refreshToken]; consumed {
			s.revokeFamilyLocked(familyID)
		}
		return "", "", "", 0, false
	}
	if re.ClientID != clientID {
		return "", "", "", 0, false
	}
	delete(s.refresh, refreshToken)
	if time.Now().After(re.ExpiresAt) {
		return "", "", "", 0, false
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
	}
	s.refresh[rt] = &refreshEntry{
		Token:     rt,
		ClientID:  clientID,
		Scope:     re.Scope,
		ExpiresAt: now.Add(refreshTokenTTL),
		FamilyID:  re.FamilyID,
	}
	if c := s.clients[clientID]; c != nil {
		c.LastActivity = now
	}
	return re.Scope, at, rt, int(accessTokenTTL.Seconds()), true
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
func (s *store) startEvictor(interval time.Duration) func() {
	ticker := time.NewTicker(interval)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-ticker.C:
				s.evict()
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()

	return func() { close(done) }
}

func (s *store) evict() {
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
	// Evict idle clients — clients with no token/code activity for clientIdleTTL.
	// Authorized (statically configured) clients are never evicted.
	cutoff := now.Add(-clientIdleTTL)
	for k, c := range s.clients {
		if c.Authorized {
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
}
