-- +goose Up
-- Add new schema named "auth"
CREATE SCHEMA IF NOT EXISTS "auth";
-- Create "identity" table
CREATE TABLE "auth"."identity" (
    "id" bigint NOT NULL GENERATED ALWAYS AS IDENTITY,
    "provider" text NOT NULL,
    "subject" text NOT NULL,
    "email_normalized" text NULL,
    "email_verified" boolean NOT NULL DEFAULT false,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "updated_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    CONSTRAINT "uq_auth_identity_provider_subject" UNIQUE ("provider", "subject"),
    CONSTRAINT "chk_auth_identity_provider" CHECK (btrim(provider) <> ''::text),
    CONSTRAINT "chk_auth_identity_subject" CHECK (btrim(subject) <> ''::text),
    CONSTRAINT "chk_auth_identity_email_normalized" CHECK (
        (email_normalized IS NULL)
        OR ((email_normalized <> ''::text) AND (email_normalized = lower(btrim(email_normalized))))
    ),
    CONSTRAINT "chk_auth_identity_verified_email" CHECK (
        (email_verified = false) OR (email_normalized IS NOT NULL)
    )
);
-- Create index "idx_auth_identity_email_normalized" to table: "identity"
CREATE INDEX "idx_auth_identity_email_normalized"
    ON "auth"."identity" ("email_normalized")
    WHERE email_normalized IS NOT NULL;
-- Create "oauth_client" table
CREATE TABLE "auth"."oauth_client" (
    "client_id" text NOT NULL,
    "secret_hash" bytea NULL,
    "redirect_uris" text[] NOT NULL,
    "name" text NOT NULL DEFAULT '',
    "is_public" boolean NOT NULL DEFAULT false,
    "is_static" boolean NOT NULL DEFAULT false,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "updated_at" timestamptz NOT NULL DEFAULT now(),
    "last_activity_at" timestamptz NOT NULL DEFAULT now(),
    "metadata_refreshed_at" timestamptz NULL,
    PRIMARY KEY ("client_id"),
    CONSTRAINT "chk_auth_oauth_client_id" CHECK (btrim(client_id) <> ''::text),
    CONSTRAINT "chk_auth_oauth_client_redirects" CHECK (cardinality(redirect_uris) > 0),
    CONSTRAINT "chk_auth_oauth_client_secret" CHECK (
        ((is_public = true) AND (secret_hash IS NULL))
        OR
        ((is_public = false) AND (secret_hash IS NOT NULL) AND (octet_length(secret_hash) > 0))
    )
);
-- Create index "idx_auth_oauth_client_last_activity" to table: "oauth_client"
CREATE INDEX "idx_auth_oauth_client_last_activity"
    ON "auth"."oauth_client" ("last_activity_at")
    WHERE is_static = false;
-- Create "authorization_code" table
CREATE TABLE "auth"."authorization_code" (
    "code_hash" bytea NOT NULL,
    "identity_id" bigint NOT NULL,
    "client_id" text NOT NULL,
    "redirect_uri" text NOT NULL,
    "code_challenge" text NOT NULL,
    "code_challenge_method" text NOT NULL,
    "scope" text[] NOT NULL DEFAULT '{}',
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "expires_at" timestamptz NOT NULL,
    PRIMARY KEY ("code_hash"),
    CONSTRAINT "fk_auth_authorization_code_identity" FOREIGN KEY ("identity_id")
        REFERENCES "auth"."identity" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "fk_auth_authorization_code_client" FOREIGN KEY ("client_id")
        REFERENCES "auth"."oauth_client" ("client_id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "chk_auth_authorization_code_hash" CHECK (octet_length(code_hash) = 32),
    CONSTRAINT "chk_auth_authorization_code_pkce" CHECK (
        (code_challenge <> ''::text) AND (code_challenge_method = 'S256'::text)
    )
);
-- Create index "idx_auth_authorization_code_identity" to table: "authorization_code"
CREATE INDEX "idx_auth_authorization_code_identity"
    ON "auth"."authorization_code" ("identity_id");
-- Create index "idx_auth_authorization_code_client" to table: "authorization_code"
CREATE INDEX "idx_auth_authorization_code_client"
    ON "auth"."authorization_code" ("client_id");
-- Create index "idx_auth_authorization_code_expires" to table: "authorization_code"
CREATE INDEX "idx_auth_authorization_code_expires"
    ON "auth"."authorization_code" ("expires_at");
-- Create "token_family" table
CREATE TABLE "auth"."token_family" (
    "family_id" text NOT NULL,
    "identity_id" bigint NOT NULL,
    "client_id" text NOT NULL,
    "scope" text[] NOT NULL DEFAULT '{}',
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "last_activity_at" timestamptz NOT NULL DEFAULT now(),
    "expires_at" timestamptz NOT NULL,
    "revoked_at" timestamptz NULL,
    PRIMARY KEY ("family_id"),
    CONSTRAINT "fk_auth_token_family_identity" FOREIGN KEY ("identity_id")
        REFERENCES "auth"."identity" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "fk_auth_token_family_client" FOREIGN KEY ("client_id")
        REFERENCES "auth"."oauth_client" ("client_id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "chk_auth_token_family_id" CHECK (btrim(family_id) <> ''::text)
);
-- Create index "idx_auth_token_family_identity" to table: "token_family"
CREATE INDEX "idx_auth_token_family_identity"
    ON "auth"."token_family" ("identity_id");
-- Create index "idx_auth_token_family_client" to table: "token_family"
CREATE INDEX "idx_auth_token_family_client"
    ON "auth"."token_family" ("client_id");
-- Create index "idx_auth_token_family_expires" to table: "token_family"
CREATE INDEX "idx_auth_token_family_expires"
    ON "auth"."token_family" ("expires_at");
-- Create index "idx_auth_token_family_revoked" to table: "token_family"
CREATE INDEX "idx_auth_token_family_revoked"
    ON "auth"."token_family" ("revoked_at")
    WHERE revoked_at IS NOT NULL;
-- Create "access_token" table
CREATE TABLE "auth"."access_token" (
    "token_hash" bytea NOT NULL,
    "family_id" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "expires_at" timestamptz NOT NULL,
    "last_used_at" timestamptz NULL,
    PRIMARY KEY ("token_hash"),
    CONSTRAINT "fk_auth_access_token_family" FOREIGN KEY ("family_id")
        REFERENCES "auth"."token_family" ("family_id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "chk_auth_access_token_hash" CHECK (octet_length(token_hash) = 32)
);
-- Create index "idx_auth_access_token_family" to table: "access_token"
CREATE INDEX "idx_auth_access_token_family"
    ON "auth"."access_token" ("family_id");
-- Create index "idx_auth_access_token_expires" to table: "access_token"
CREATE INDEX "idx_auth_access_token_expires"
    ON "auth"."access_token" ("expires_at");
-- Create "refresh_token" table
CREATE TABLE "auth"."refresh_token" (
    "token_hash" bytea NOT NULL,
    "family_id" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "expires_at" timestamptz NOT NULL,
    "consumed_at" timestamptz NULL,
    PRIMARY KEY ("token_hash"),
    CONSTRAINT "fk_auth_refresh_token_family" FOREIGN KEY ("family_id")
        REFERENCES "auth"."token_family" ("family_id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "chk_auth_refresh_token_hash" CHECK (octet_length(token_hash) = 32)
);
-- Create index "idx_auth_refresh_token_family" to table: "refresh_token"
CREATE INDEX "idx_auth_refresh_token_family"
    ON "auth"."refresh_token" ("family_id");
-- Create index "uq_auth_refresh_token_live_family" to table: "refresh_token"
CREATE UNIQUE INDEX "uq_auth_refresh_token_live_family"
    ON "auth"."refresh_token" ("family_id")
    WHERE consumed_at IS NULL;
-- Create index "idx_auth_refresh_token_live_expires" to table: "refresh_token"
CREATE INDEX "idx_auth_refresh_token_live_expires"
    ON "auth"."refresh_token" ("expires_at")
    WHERE consumed_at IS NULL;
-- Create index "idx_auth_refresh_token_consumed" to table: "refresh_token"
CREATE INDEX "idx_auth_refresh_token_consumed"
    ON "auth"."refresh_token" ("consumed_at")
    WHERE consumed_at IS NOT NULL;
