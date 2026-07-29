package oauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const clientRegistrationLockSQL = `
SELECT pg_advisory_xact_lock(hashtextextended('compliary.oauth.client-registration', 0))`

type postgresStore struct {
	pool *pgxpool.Pool
}

func newPostgresStore(pool *pgxpool.Pool) *postgresStore {
	return &postgresStore{pool: pool}
}

func (s *postgresStore) checkSchema(ctx context.Context) error {
	var tableName *string
	if err := s.pool.QueryRow(ctx,
		`SELECT to_regclass('auth.oauth_client')::text`,
	).Scan(&tableName); err != nil {
		return fmt.Errorf("check OAuth database schema: %w", err)
	}
	if tableName == nil {
		return errors.New("OAuth database schema is missing; run cmd/migrate")
	}
	return nil
}

func (s *postgresStore) registerClient(ctx context.Context, meta oauthex.ClientRegistrationMetadata) (*oauthex.ClientRegistrationResponse, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin client registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, clientRegistrationLockSQL); err != nil {
		return nil, fmt.Errorf("lock client registration: %w", err)
	}
	if err := evictExpired(ctx, tx); err != nil {
		return nil, err
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM auth.oauth_client`).Scan(&count); err != nil {
		return nil, fmt.Errorf("count OAuth clients: %w", err)
	}
	if count >= maxClients {
		return nil, errClientCapReached
	}

	clientID := randomHex(16)
	clientSecret := randomHex(32)
	issuedAt := time.Now()
	_, err = tx.Exec(ctx, `
INSERT INTO auth.oauth_client
    (client_id, secret_hash, redirect_uris, name, is_public, is_static)
VALUES ($1, $2, $3, $4, FALSE, FALSE)`,
		clientID, hashToken(clientSecret), meta.RedirectURIs, meta.ClientName,
	)
	if err != nil {
		return nil, fmt.Errorf("insert OAuth client: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit client registration: %w", err)
	}

	return &oauthex.ClientRegistrationResponse{
		ClientRegistrationMetadata: meta,
		ClientID:                   clientID,
		ClientSecret:               clientSecret,
		ClientIDIssuedAt:           issuedAt,
	}, nil
}

func (s *postgresStore) lookupClient(ctx context.Context, clientID string) (*client, error) {
	var c client
	err := s.pool.QueryRow(ctx, `
SELECT client_id, secret_hash, redirect_uris, name, is_public, is_static,
       created_at, last_activity_at, metadata_refreshed_at
FROM auth.oauth_client
WHERE client_id = $1`, clientID).Scan(
		&c.ID,
		&c.SecretHash,
		&c.RedirectURIs,
		&c.Name,
		&c.Public,
		&c.Authorized,
		&c.CreatedAt,
		&c.LastActivity,
		&c.MetadataRefreshedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup OAuth client: %w", err)
	}
	return &c, nil
}

func (s *postgresStore) registerCIMDClient(ctx context.Context, clientID string, meta *oauthex.ClientRegistrationMetadata) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin CIMD registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, clientRegistrationLockSQL); err != nil {
		return fmt.Errorf("lock CIMD registration: %w", err)
	}
	if err := evictExpired(ctx, tx); err != nil {
		return err
	}

	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM auth.oauth_client WHERE client_id = $1)`,
		clientID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check CIMD client: %w", err)
	}
	if !exists {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM auth.oauth_client`).Scan(&count); err != nil {
			return fmt.Errorf("count OAuth clients: %w", err)
		}
		if count >= maxClients {
			return errClientCapReached
		}
	}

	_, err = tx.Exec(ctx, `
INSERT INTO auth.oauth_client
    (client_id, secret_hash, redirect_uris, name, is_public, is_static,
     metadata_refreshed_at)
VALUES ($1, NULL, $2, $3, TRUE, FALSE, now())
ON CONFLICT (client_id) DO UPDATE SET
    secret_hash = NULL,
    redirect_uris = EXCLUDED.redirect_uris,
    name = EXCLUDED.name,
    is_public = TRUE,
    metadata_refreshed_at = now(),
    updated_at = now(),
    last_activity_at = now()`,
		clientID, meta.RedirectURIs, meta.ClientName,
	)
	if err != nil {
		return fmt.Errorf("upsert CIMD client: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit CIMD registration: %w", err)
	}
	return nil
}

func (s *postgresStore) createCode(
	ctx context.Context,
	clientID, redirectURI, codeChallenge, challengeMethod, scope string,
	principal Principal,
) (string, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("begin authorization code creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	identityID, err := upsertIdentity(ctx, tx, principal)
	if err != nil {
		return "", err
	}

	code := randomHex(32)
	_, err = tx.Exec(ctx, `
INSERT INTO auth.authorization_code
    (code_hash, identity_id, client_id, redirect_uri, code_challenge,
     code_challenge_method, scope, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now() + $8::interval)`,
		hashToken(code),
		identityID,
		clientID,
		redirectURI,
		codeChallenge,
		challengeMethod,
		strings.Fields(scope),
		durationInterval(authCodeTTL),
	)
	if err != nil {
		return "", fmt.Errorf("insert authorization code: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE auth.oauth_client
SET last_activity_at = now(), updated_at = now()
WHERE client_id = $1`, clientID); err != nil {
		return "", fmt.Errorf("touch OAuth client: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit authorization code creation: %w", err)
	}
	return code, nil
}

func (s *postgresStore) exchangeCode(
	ctx context.Context,
	code, clientID, redirectURI, codeVerifier string,
) (scope, accessToken, refreshToken string, expiresIn int, ok bool, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", "", 0, false, fmt.Errorf("begin authorization code exchange: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		identityID     int64
		storedClientID string
		storedRedirect string
		codeChallenge  string
		scopes         []string
		expiresAt      time.Time
		dbNow          time.Time
	)
	err = tx.QueryRow(ctx, `
SELECT identity_id, client_id, redirect_uri, code_challenge, scope, expires_at,
       clock_timestamp()
FROM auth.authorization_code
WHERE code_hash = $1
FOR UPDATE`, hashToken(code)).Scan(
		&identityID,
		&storedClientID,
		&storedRedirect,
		&codeChallenge,
		&scopes,
		&expiresAt,
		&dbNow,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", 0, false, nil
	}
	if err != nil {
		return "", "", "", 0, false, fmt.Errorf("lock authorization code: %w", err)
	}

	// Consume the code for every exchange attempt, including invalid ones.
	if _, err := tx.Exec(ctx,
		`DELETE FROM auth.authorization_code WHERE code_hash = $1`,
		hashToken(code),
	); err != nil {
		return "", "", "", 0, false, fmt.Errorf("consume authorization code: %w", err)
	}

	valid := expiresAt.After(dbNow) &&
		storedClientID == clientID &&
		storedRedirect == redirectURI &&
		verifyPKCE(codeVerifier, codeChallenge)
	if !valid {
		if err := tx.Commit(ctx); err != nil {
			return "", "", "", 0, false, fmt.Errorf("commit rejected code exchange: %w", err)
		}
		return "", "", "", 0, false, nil
	}

	familyID := randomHex(16)
	at := randomHex(32)
	rt := randomHex(32)
	tokenExpiresAt := dbNow.Add(tokenIdleTTL)
	if err := insertTokenPair(
		ctx, tx, familyID, identityID, clientID, scopes,
		at, rt, dbNow, tokenExpiresAt,
	); err != nil {
		return "", "", "", 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", "", 0, false, fmt.Errorf("commit authorization code exchange: %w", err)
	}
	return strings.Join(scopes, " "), at, rt, int(accessTokenTTL.Seconds()), true, nil
}

func (s *postgresStore) lookupToken(ctx context.Context, accessToken string) (*tokenEntry, bool, error) {
	var (
		entry           tokenEntry
		scopes          []string
		emailNormalized *string
	)
	err := s.pool.QueryRow(ctx, `
SELECT a.family_id, f.client_id, f.scope, a.expires_at,
       i.provider, i.subject, i.email_normalized, i.email_verified
FROM auth.access_token AS a
JOIN auth.token_family AS f ON f.family_id = a.family_id
JOIN auth.identity AS i ON i.id = f.identity_id
WHERE a.token_hash = $1
  AND a.expires_at > clock_timestamp()
  AND f.expires_at > clock_timestamp()
  AND f.revoked_at IS NULL`, hashToken(accessToken)).Scan(
		&entry.FamilyID,
		&entry.ClientID,
		&scopes,
		&entry.ExpiresAt,
		&entry.Principal.Provider,
		&entry.Principal.Subject,
		&emailNormalized,
		&entry.Principal.EmailVerified,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("lookup access token: %w", err)
	}
	entry.Token = accessToken
	entry.Scope = strings.Join(scopes, " ")
	if emailNormalized != nil {
		entry.Principal.Email = *emailNormalized
	}
	return &entry, true, nil
}

func (s *postgresStore) renewToken(ctx context.Context, accessToken string) (*tokenEntry, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("begin token lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		entry           tokenEntry
		scopes          []string
		accessExpiresAt time.Time
		familyExpiresAt time.Time
		revokedAt       *time.Time
		emailNormalized *string
		dbNow           time.Time
	)
	err = tx.QueryRow(ctx, `
SELECT family_id
FROM auth.access_token
WHERE token_hash = $1`, hashToken(accessToken)).Scan(&entry.FamilyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("locate access-token family: %w", err)
	}

	// Every family operation locks the family row first. Refresh rotation uses
	// the same order before locking its refresh row, preventing a deadlock with
	// this transaction's later refresh-expiry update.
	err = tx.QueryRow(ctx, `
SELECT f.client_id, f.scope, f.expires_at, f.revoked_at,
       i.provider, i.subject, i.email_normalized,
       i.email_verified, clock_timestamp()
FROM auth.token_family AS f
JOIN auth.identity AS i ON i.id = f.identity_id
WHERE f.family_id = $1
FOR UPDATE OF f`, entry.FamilyID).Scan(
		&entry.ClientID,
		&scopes,
		&familyExpiresAt,
		&revokedAt,
		&entry.Principal.Provider,
		&entry.Principal.Subject,
		&emailNormalized,
		&entry.Principal.EmailVerified,
		&dbNow,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("lock access-token family: %w", err)
	}
	if revokedAt != nil || !familyExpiresAt.After(dbNow) {
		return nil, false, nil
	}

	err = tx.QueryRow(ctx, `
SELECT expires_at
FROM auth.access_token
WHERE token_hash = $1 AND family_id = $2
FOR UPDATE`, hashToken(accessToken), entry.FamilyID).Scan(&accessExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("lock access token: %w", err)
	}
	if !accessExpiresAt.After(dbNow) {
		return nil, false, nil
	}

	newExpiry := dbNow.Add(tokenIdleTTL)
	if _, err := tx.Exec(ctx, `
UPDATE auth.access_token
SET expires_at = $2, last_used_at = $3
WHERE token_hash = $1`, hashToken(accessToken), newExpiry, dbNow); err != nil {
		return nil, false, fmt.Errorf("renew access token: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE auth.token_family
SET expires_at = $2, last_activity_at = $3
WHERE family_id = $1`, entry.FamilyID, newExpiry, dbNow); err != nil {
		return nil, false, fmt.Errorf("renew token family: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE auth.refresh_token
SET expires_at = $2
WHERE family_id = $1
  AND consumed_at IS NULL
  AND expires_at > $3`, entry.FamilyID, newExpiry, dbNow); err != nil {
		return nil, false, fmt.Errorf("renew refresh token: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE auth.oauth_client
SET last_activity_at = $2, updated_at = $2
WHERE client_id = $1`, entry.ClientID, dbNow); err != nil {
		return nil, false, fmt.Errorf("touch OAuth client: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit token renewal: %w", err)
	}

	entry.Token = accessToken
	entry.Scope = strings.Join(scopes, " ")
	entry.ExpiresAt = newExpiry
	if emailNormalized != nil {
		entry.Principal.Email = *emailNormalized
	}
	return &entry, true, nil
}

func (s *postgresStore) rotateRefresh(
	ctx context.Context,
	refreshToken, clientID string,
	authorizer Authorizer,
) (scope, accessToken, newRefresh string, expiresIn int, ok bool, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", "", 0, false, fmt.Errorf("begin refresh rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		familyID        string
		storedClientID  string
		scopes          []string
		refreshExpires  time.Time
		familyExpires   time.Time
		consumedAt      *time.Time
		revokedAt       *time.Time
		emailNormalized *string
		principal       Principal
		dbNow           time.Time
	)
	err = tx.QueryRow(ctx, `
SELECT family_id
FROM auth.refresh_token
WHERE token_hash = $1`, hashToken(refreshToken)).Scan(&familyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", 0, false, nil
	}
	if err != nil {
		return "", "", "", 0, false, fmt.Errorf("locate refresh-token family: %w", err)
	}

	// Lock the family before the refresh row. Access-token renewal follows the
	// same family-first order before touching the live refresh token.
	err = tx.QueryRow(ctx, `
SELECT f.client_id, f.scope, f.expires_at, f.revoked_at,
       i.provider, i.subject, i.email_normalized, i.email_verified,
       clock_timestamp()
FROM auth.token_family AS f
JOIN auth.identity AS i ON i.id = f.identity_id
WHERE f.family_id = $1
FOR UPDATE OF f`, familyID).Scan(
		&storedClientID,
		&scopes,
		&familyExpires,
		&revokedAt,
		&principal.Provider,
		&principal.Subject,
		&emailNormalized,
		&principal.EmailVerified,
		&dbNow,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", 0, false, nil
	}
	if err != nil {
		return "", "", "", 0, false, fmt.Errorf("lock refresh-token family: %w", err)
	}
	if storedClientID != clientID {
		return "", "", "", 0, false, nil
	}

	err = tx.QueryRow(ctx, `
SELECT expires_at, consumed_at
FROM auth.refresh_token
WHERE token_hash = $1 AND family_id = $2
FOR UPDATE`, hashToken(refreshToken), familyID).Scan(&refreshExpires, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", 0, false, nil
	}
	if err != nil {
		return "", "", "", 0, false, fmt.Errorf("lock refresh token: %w", err)
	}

	if consumedAt != nil {
		if _, err := tx.Exec(ctx, `
UPDATE auth.token_family
SET revoked_at = COALESCE(revoked_at, $2), last_activity_at = $2
WHERE family_id = $1`, familyID, dbNow); err != nil {
			return "", "", "", 0, false, fmt.Errorf("revoke replayed token family: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return "", "", "", 0, false, fmt.Errorf("commit replay revocation: %w", err)
		}
		return "", "", "", 0, false, nil
	}
	if revokedAt != nil || !refreshExpires.After(dbNow) || !familyExpires.After(dbNow) {
		return "", "", "", 0, false, nil
	}
	if emailNormalized != nil {
		principal.Email = *emailNormalized
	}
	if authorizer != nil {
		if err := authorizer.Authorize(ctx, principal); err != nil {
			return "", "", "", 0, false, nil
		}
	}

	at := randomHex(32)
	rt := randomHex(32)
	newExpiry := dbNow.Add(tokenIdleTTL)
	if _, err := tx.Exec(ctx, `
UPDATE auth.refresh_token
SET consumed_at = $2
WHERE token_hash = $1`, hashToken(refreshToken), dbNow); err != nil {
		return "", "", "", 0, false, fmt.Errorf("consume refresh token: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO auth.access_token (token_hash, family_id, created_at, expires_at)
VALUES ($1, $2, $3, $4)`,
		hashToken(at), familyID, dbNow, newExpiry,
	); err != nil {
		return "", "", "", 0, false, fmt.Errorf("insert rotated access token: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO auth.refresh_token (token_hash, family_id, created_at, expires_at)
VALUES ($1, $2, $3, $4)`,
		hashToken(rt), familyID, dbNow, newExpiry,
	); err != nil {
		return "", "", "", 0, false, fmt.Errorf("insert rotated refresh token: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE auth.token_family
SET expires_at = $2, last_activity_at = $3
WHERE family_id = $1`, familyID, newExpiry, dbNow); err != nil {
		return "", "", "", 0, false, fmt.Errorf("renew rotated token family: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE auth.oauth_client
SET last_activity_at = $2, updated_at = $2
WHERE client_id = $1`, clientID, dbNow); err != nil {
		return "", "", "", 0, false, fmt.Errorf("touch OAuth client: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", "", 0, false, fmt.Errorf("commit refresh rotation: %w", err)
	}
	return strings.Join(scopes, " "), at, rt, int(accessTokenTTL.Seconds()), true, nil
}

func (s *postgresStore) evict(ctx context.Context) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin OAuth cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := evictExpired(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit OAuth cleanup: %w", err)
	}
	return nil
}

func upsertIdentity(ctx context.Context, tx pgx.Tx, principal Principal) (int64, error) {
	var email *string
	if normalized := strings.ToLower(strings.TrimSpace(principal.Email)); normalized != "" {
		email = &normalized
	}

	var identityID int64
	err := tx.QueryRow(ctx, `
INSERT INTO auth.identity (provider, subject, email_normalized, email_verified)
VALUES ($1, $2, $3, $4)
ON CONFLICT (provider, subject) DO UPDATE SET
    email_normalized = COALESCE(EXCLUDED.email_normalized, auth.identity.email_normalized),
    email_verified = CASE
        WHEN EXCLUDED.email_normalized IS NULL THEN auth.identity.email_verified
        ELSE EXCLUDED.email_verified
    END,
    updated_at = now()
RETURNING id`,
		principal.Provider,
		principal.Subject,
		email,
		principal.EmailVerified && email != nil,
	).Scan(&identityID)
	if err != nil {
		return 0, fmt.Errorf("upsert OAuth identity: %w", err)
	}
	return identityID, nil
}

func insertTokenPair(
	ctx context.Context,
	tx pgx.Tx,
	familyID string,
	identityID int64,
	clientID string,
	scopes []string,
	accessToken string,
	refreshToken string,
	createdAt time.Time,
	expiresAt time.Time,
) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO auth.token_family
    (family_id, identity_id, client_id, scope, created_at, last_activity_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $5, $6)`,
		familyID, identityID, clientID, scopes, createdAt, expiresAt,
	); err != nil {
		return fmt.Errorf("insert token family: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO auth.access_token (token_hash, family_id, created_at, expires_at)
VALUES ($1, $2, $3, $4)`,
		hashToken(accessToken), familyID, createdAt, expiresAt,
	); err != nil {
		return fmt.Errorf("insert access token: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO auth.refresh_token (token_hash, family_id, created_at, expires_at)
VALUES ($1, $2, $3, $4)`,
		hashToken(refreshToken), familyID, createdAt, expiresAt,
	); err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE auth.oauth_client
SET last_activity_at = $2, updated_at = $2
WHERE client_id = $1`, clientID, createdAt); err != nil {
		return fmt.Errorf("touch OAuth client: %w", err)
	}
	return nil
}

func evictExpired(ctx context.Context, tx pgx.Tx) error {
	statements := []string{
		`DELETE FROM auth.authorization_code WHERE expires_at <= now()`,
		`DELETE FROM auth.access_token WHERE expires_at <= now()`,
		`DELETE FROM auth.token_family WHERE expires_at <= now()`,
		`DELETE FROM auth.oauth_client AS c
		 WHERE c.is_static = FALSE
		   AND c.last_activity_at < now() - INTERVAL '24 hours'
		   AND NOT EXISTS (
		       SELECT 1 FROM auth.authorization_code AS ac
		       WHERE ac.client_id = c.client_id
		   )
		   AND NOT EXISTS (
		       SELECT 1 FROM auth.token_family AS tf
		       WHERE tf.client_id = c.client_id
		         AND tf.revoked_at IS NULL
		         AND tf.expires_at > now()
		   )`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("evict expired OAuth state: %w", err)
		}
	}
	return nil
}

// durationInterval uses PostgreSQL's unambiguous interval text input.
func durationInterval(d time.Duration) string {
	return fmt.Sprintf("%f seconds", d.Seconds())
}
