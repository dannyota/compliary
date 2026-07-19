// Package oauth implements an OAuth 2.0 authorization server for compliary's
// single-user MCP endpoint. It uses opaque tokens with in-memory storage —
// no JWT signing, no JWKS keys, no external token introspection.
package oauth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	accessTokenTTL  = 1 * time.Hour
	refreshTokenTTL = 7 * 24 * time.Hour
	authCodeTTL     = 10 * time.Minute
)

type store struct {
	mu      sync.Mutex
	clients map[string]*client       // client_id -> client
	codes   map[string]*authCode     // code -> authCode
	tokens  map[string]*tokenEntry   // access_token -> tokenEntry
	refresh map[string]*refreshEntry // refresh_token -> refreshEntry
}

type client struct {
	ID           string
	Secret       string
	RedirectURIs []string
	Name         string
	Public       bool // true for CIMD clients (no secret)
	CreatedAt    time.Time
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
}

type refreshEntry struct {
	Token     string
	ClientID  string
	Scope     string
	ExpiresAt time.Time
}

func newStore() *store {
	return &store{
		clients: make(map[string]*client),
		codes:   make(map[string]*authCode),
		tokens:  make(map[string]*tokenEntry),
		refresh: make(map[string]*refreshEntry),
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func (s *store) registerClient(meta oauthex.ClientRegistrationMetadata) *oauthex.ClientRegistrationResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

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
	}
}

func (s *store) lookupClient(clientID string) *client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clients[clientID]
}

// registerCIMDClient registers a public client from a Client ID Metadata Document.
// The clientID is the HTTPS URL of the metadata document itself.
func (s *store) registerCIMDClient(clientID string, meta *oauthex.ClientRegistrationMetadata) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clients[clientID] = &client{
		ID:           clientID,
		RedirectURIs: meta.RedirectURIs,
		Name:         meta.ClientName,
		Public:       true,
		CreatedAt:    time.Now(),
	}
}

func (s *store) createCode(clientID, redirectURI, codeChallenge, challengeMethod, scope string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	code := randomHex(32)
	s.codes[code] = &authCode{
		Code:            code,
		ClientID:        clientID,
		RedirectURI:     redirectURI,
		CodeChallenge:   codeChallenge,
		ChallengeMethod: challengeMethod,
		Scope:           scope,
		ExpiresAt:       time.Now().Add(authCodeTTL),
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

func (s *store) createTokenPair(clientID, scope string) (accessToken, refreshToken string, expiresIn int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	at := randomHex(32)
	rt := randomHex(32)
	now := time.Now()

	s.tokens[at] = &tokenEntry{
		Token:     at,
		ClientID:  clientID,
		Scope:     scope,
		ExpiresAt: now.Add(accessTokenTTL),
	}
	s.refresh[rt] = &refreshEntry{
		Token:     rt,
		ClientID:  clientID,
		Scope:     scope,
		ExpiresAt: now.Add(refreshTokenTTL),
	}
	return at, rt, int(accessTokenTTL.Seconds())
}

// createTokenPairWithTTL is like createTokenPair but with a custom access token TTL.
// Used for testing token expiry.
func (s *store) createTokenPairWithTTL(clientID, scope string, atTTL time.Duration) (accessToken, refreshToken string, expiresIn int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	at := randomHex(32)
	rt := randomHex(32)
	now := time.Now()

	s.tokens[at] = &tokenEntry{
		Token:     at,
		ClientID:  clientID,
		Scope:     scope,
		ExpiresAt: now.Add(atTTL),
	}
	s.refresh[rt] = &refreshEntry{
		Token:     rt,
		ClientID:  clientID,
		Scope:     scope,
		ExpiresAt: now.Add(refreshTokenTTL),
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

func (s *store) consumeRefresh(refreshToken string) (*refreshEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	re, ok := s.refresh[refreshToken]
	if !ok {
		return nil, false
	}
	delete(s.refresh, refreshToken)
	if time.Now().After(re.ExpiresAt) {
		return nil, false
	}
	return re, true
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
}
