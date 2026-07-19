package oauth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// Server is an OAuth 2.0 authorization server for compliary's single-user
// MCP endpoint. It issues opaque tokens stored in memory and verifies the
// operator's identity via a bcrypt-hashed password.
type Server struct {
	issuer       string
	operatorHash []byte
	store        *store
	log          *slog.Logger
	stopEvictor  func()
}

// New creates a Server. issuer is the public URL of the compliary instance
// (e.g. "https://compliary.danny.vn"). operatorHash is the bcrypt hash of
// the operator's password (from COMPLIARY_OAUTH_OPERATOR_SECRET).
func New(issuer string, operatorHash []byte, log *slog.Logger) *Server {
	s := &Server{
		issuer:       issuer,
		operatorHash: operatorHash,
		store:        newStore(),
		log:          log,
	}
	s.stopEvictor = s.store.startEvictor(5 * time.Minute)
	return s
}

// StopEvictor stops the background expired-entry cleanup goroutine.
func (s *Server) StopEvictor() {
	if s.stopEvictor != nil {
		s.stopEvictor()
	}
}

// Handler returns an http.Handler with all OAuth routes mounted.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Protected Resource Metadata (RFC 9728).
	mux.Handle("GET /.well-known/oauth-protected-resource",
		auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
			Resource:               s.issuer,
			AuthorizationServers:   []string{s.issuer},
			ScopesSupported:        []string{"mcp:read"},
			BearerMethodsSupported: []string{"header"},
			ResourceName:           "compliary MCP",
			ResourceDocumentation:  "https://github.com/dannyota/compliary",
		}))

	// Authorization Server Metadata (RFC 8414).
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.authServerMetadataHandler)

	// Empty JWKS (opaque tokens).
	mux.HandleFunc("GET /oauth/jwks", s.jwksHandler)

	// Dynamic Client Registration (RFC 7591).
	mux.HandleFunc("POST /oauth/register", s.registerHandler)

	// Authorization endpoint.
	mux.HandleFunc("/oauth/authorize", s.authorizeHandler)

	// Token endpoint.
	mux.HandleFunc("POST /oauth/token", s.tokenHandler)

	return mux
}

// TokenVerifier returns an [auth.TokenVerifier] that validates opaque access
// tokens from this server's in-memory store.
func (s *Server) TokenVerifier() auth.TokenVerifier {
	return func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		te, ok := s.store.lookupToken(token)
		if !ok {
			return nil, fmt.Errorf("%w: unknown or expired token", auth.ErrInvalidToken)
		}
		return &auth.TokenInfo{
			Scopes:     splitScope(te.Scope),
			Expiration: te.ExpiresAt,
		}, nil
	}
}

// BearerFallback returns a combined [auth.TokenVerifier] that first tries
// the OAuth token store, then falls back to matching a static bearer token
// (for backward compatibility with COMPLIARY_MCP_TOKEN).
func (s *Server) BearerFallback(staticToken string) auth.TokenVerifier {
	oauthVerify := s.TokenVerifier()
	return func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		// Try OAuth store first.
		info, err := oauthVerify(ctx, token, req)
		if err == nil {
			return info, nil
		}

		// Fall back to static bearer token.
		if subtle.ConstantTimeCompare([]byte(token), []byte(staticToken)) == 1 {
			return &auth.TokenInfo{
				Scopes:     []string{"mcp:read"},
				Expiration: time.Now().Add(1 * time.Hour),
			}, nil
		}

		return nil, fmt.Errorf("%w: invalid token", auth.ErrInvalidToken)
	}
}

func splitScope(scope string) []string {
	if scope == "" {
		return nil
	}
	return strings.Fields(scope)
}
