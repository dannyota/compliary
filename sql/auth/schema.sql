CREATE SCHEMA IF NOT EXISTS auth;

-- auth.identity is the durable login identity. Authorization policy is
-- deliberately not stored here yet: any authenticated identity is accepted,
-- while email_normalized gives a later email allowlist a stable lookup key.
CREATE TABLE auth.identity (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider         TEXT NOT NULL,
    subject          TEXT NOT NULL,
    email_normalized TEXT,
    email_verified   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_auth_identity_provider_subject UNIQUE (provider, subject),
    CONSTRAINT chk_auth_identity_provider CHECK (btrim(provider) <> ''),
    CONSTRAINT chk_auth_identity_subject CHECK (btrim(subject) <> ''),
    CONSTRAINT chk_auth_identity_email_normalized CHECK (
        email_normalized IS NULL
        OR (
            email_normalized <> ''
            AND email_normalized = lower(btrim(email_normalized))
        )
    ),
    CONSTRAINT chk_auth_identity_verified_email CHECK (
        email_verified = FALSE OR email_normalized IS NOT NULL
    )
);

CREATE INDEX idx_auth_identity_email_normalized
    ON auth.identity (email_normalized)
    WHERE email_normalized IS NOT NULL;

-- auth.oauth_client persists both dynamically registered confidential clients
-- and public/static clients. Only hashes of confidential client secrets are
-- retained. last_activity_at is initialized at registration so unused,
-- non-static registrations can be cleaned up with an indexed cutoff.
CREATE TABLE auth.oauth_client (
    client_id        TEXT PRIMARY KEY,
    secret_hash      BYTEA,
    redirect_uris    TEXT[] NOT NULL,
    name             TEXT NOT NULL DEFAULT '',
    is_public        BOOLEAN NOT NULL DEFAULT FALSE,
    is_static        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    metadata_refreshed_at TIMESTAMPTZ,
    CONSTRAINT chk_auth_oauth_client_id CHECK (btrim(client_id) <> ''),
    CONSTRAINT chk_auth_oauth_client_redirects CHECK (cardinality(redirect_uris) > 0),
    CONSTRAINT chk_auth_oauth_client_secret CHECK (
        (is_public = TRUE AND secret_hash IS NULL)
        OR
        (is_public = FALSE AND secret_hash IS NOT NULL AND octet_length(secret_hash) > 0)
    )
);

CREATE INDEX idx_auth_oauth_client_last_activity
    ON auth.oauth_client (last_activity_at)
    WHERE is_static = FALSE;

-- Authorization codes are stored only by SHA-256 hash. Deleting an identity or
-- client removes its outstanding codes.
CREATE TABLE auth.authorization_code (
    code_hash             BYTEA PRIMARY KEY,
    identity_id           BIGINT NOT NULL,
    client_id             TEXT NOT NULL,
    redirect_uri          TEXT NOT NULL,
    code_challenge        TEXT NOT NULL,
    code_challenge_method TEXT NOT NULL,
    scope                 TEXT[] NOT NULL DEFAULT '{}',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at            TIMESTAMPTZ NOT NULL,
    CONSTRAINT fk_auth_authorization_code_identity FOREIGN KEY (identity_id)
        REFERENCES auth.identity (id) ON DELETE CASCADE,
    CONSTRAINT fk_auth_authorization_code_client FOREIGN KEY (client_id)
        REFERENCES auth.oauth_client (client_id) ON DELETE CASCADE,
    CONSTRAINT chk_auth_authorization_code_hash CHECK (octet_length(code_hash) = 32),
    CONSTRAINT chk_auth_authorization_code_pkce CHECK (
        code_challenge <> '' AND code_challenge_method = 'S256'
    )
);

CREATE INDEX idx_auth_authorization_code_identity
    ON auth.authorization_code (identity_id);
CREATE INDEX idx_auth_authorization_code_client
    ON auth.authorization_code (client_id);
CREATE INDEX idx_auth_authorization_code_expires
    ON auth.authorization_code (expires_at);

-- A token family is the revocation boundary for an initial authorization-code
-- exchange and every subsequent refresh rotation. expires_at and
-- last_activity_at move together when the sliding session is used.
CREATE TABLE auth.token_family (
    family_id        TEXT PRIMARY KEY,
    identity_id      BIGINT NOT NULL,
    client_id        TEXT NOT NULL,
    scope            TEXT[] NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL,
    revoked_at       TIMESTAMPTZ,
    CONSTRAINT fk_auth_token_family_identity FOREIGN KEY (identity_id)
        REFERENCES auth.identity (id) ON DELETE CASCADE,
    CONSTRAINT fk_auth_token_family_client FOREIGN KEY (client_id)
        REFERENCES auth.oauth_client (client_id) ON DELETE CASCADE,
    CONSTRAINT chk_auth_token_family_id CHECK (btrim(family_id) <> '')
);

CREATE INDEX idx_auth_token_family_identity
    ON auth.token_family (identity_id);
CREATE INDEX idx_auth_token_family_client
    ON auth.token_family (client_id);
CREATE INDEX idx_auth_token_family_expires
    ON auth.token_family (expires_at);
CREATE INDEX idx_auth_token_family_revoked
    ON auth.token_family (revoked_at)
    WHERE revoked_at IS NOT NULL;

-- Access tokens are opaque bearer values stored only by SHA-256 hash.
CREATE TABLE auth.access_token (
    token_hash   BYTEA PRIMARY KEY,
    family_id   TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    CONSTRAINT fk_auth_access_token_family FOREIGN KEY (family_id)
        REFERENCES auth.token_family (family_id) ON DELETE CASCADE,
    CONSTRAINT chk_auth_access_token_hash CHECK (octet_length(token_hash) = 32)
);

CREATE INDEX idx_auth_access_token_family
    ON auth.access_token (family_id);
CREATE INDEX idx_auth_access_token_expires
    ON auth.access_token (expires_at);

-- Consumed refresh-token hashes remain until their family is cleaned up so a
-- replay can be distinguished from a random invalid token and revoke the
-- family. Only rows with consumed_at IS NULL are live refresh tokens.
CREATE TABLE auth.refresh_token (
    token_hash  BYTEA PRIMARY KEY,
    family_id   TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    CONSTRAINT fk_auth_refresh_token_family FOREIGN KEY (family_id)
        REFERENCES auth.token_family (family_id) ON DELETE CASCADE,
    CONSTRAINT chk_auth_refresh_token_hash CHECK (octet_length(token_hash) = 32)
);

CREATE INDEX idx_auth_refresh_token_family
    ON auth.refresh_token (family_id);
CREATE UNIQUE INDEX uq_auth_refresh_token_live_family
    ON auth.refresh_token (family_id)
    WHERE consumed_at IS NULL;
CREATE INDEX idx_auth_refresh_token_live_expires
    ON auth.refresh_token (expires_at)
    WHERE consumed_at IS NULL;
CREATE INDEX idx_auth_refresh_token_consumed
    ON auth.refresh_token (consumed_at)
    WHERE consumed_at IS NOT NULL;
