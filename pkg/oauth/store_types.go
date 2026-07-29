package oauth

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// Principal is the stable identity bound to an OAuth grant. Authorization
// decisions use Provider+Subject; email is optional profile data and must never
// be used as the token or identity key because it can change.
type Principal struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
}

// Authorizer decides whether an authenticated principal may use compliary.
// The default is AllowAllAuthorizer; keeping this boundary separate from the
// token store lets a future email policy take effect without redesigning token
// persistence. Implementations must be fast and non-reentrant: refresh checks
// run while token-family locks are held so the policy decision and rotation
// remain atomic.
type Authorizer interface {
	Authorize(context.Context, Principal) error
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(context.Context, Principal) error

func (f AuthorizerFunc) Authorize(ctx context.Context, principal Principal) error {
	return f(ctx, principal)
}

// AllowAllAuthorizer permits every successfully authenticated principal.
type AllowAllAuthorizer struct{}

func (AllowAllAuthorizer) Authorize(context.Context, Principal) error { return nil }

var errClientCapReached = errors.New("client registration cap reached")

// oauthStore is implemented by the PostgreSQL production store and the
// in-memory test store. Opaque credentials are passed in plaintext only at the
// boundary; the PostgreSQL implementation persists SHA-256 hashes.
type oauthStore interface {
	registerClient(context.Context, oauthex.ClientRegistrationMetadata) (*oauthex.ClientRegistrationResponse, error)
	lookupClient(context.Context, string) (*client, error)
	registerCIMDClient(context.Context, string, *oauthex.ClientRegistrationMetadata) error

	createCode(context.Context, string, string, string, string, string, Principal) (string, error)
	exchangeCode(context.Context, string, string, string, string) (string, string, string, int, bool, error)
	lookupToken(context.Context, string) (*tokenEntry, bool, error)
	renewToken(context.Context, string) (*tokenEntry, bool, error)
	rotateRefresh(context.Context, string, string, Authorizer) (string, string, string, int, bool, error)

	evict(context.Context) error
}

type client struct {
	ID                  string
	SecretHash          []byte
	RedirectURIs        []string
	Name                string
	Public              bool
	CreatedAt           time.Time
	LastActivity        time.Time
	MetadataRefreshedAt *time.Time
	Authorized          bool
}

type authCode struct {
	Code            string
	ClientID        string
	RedirectURI     string
	CodeChallenge   string
	ChallengeMethod string
	Scope           string
	ExpiresAt       time.Time
	Principal       Principal
}

type tokenEntry struct {
	Token     string
	ClientID  string
	Scope     string
	ExpiresAt time.Time
	FamilyID  string
	Principal Principal
}

type refreshEntry struct {
	Token     string
	ClientID  string
	Scope     string
	ExpiresAt time.Time
	FamilyID  string
	Principal Principal
}
