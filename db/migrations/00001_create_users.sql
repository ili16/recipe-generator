-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Identity comes only from the OIDC subject claim; name/email are optional claims.
CREATE TABLE users
(
    id             SERIAL PRIMARY KEY,
    oauth_id       TEXT        NOT NULL UNIQUE,
    name           TEXT,
    email          TEXT,
    oauth_provider TEXT        NOT NULL DEFAULT 'keycloak',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_last_seen_at ON users (last_seen_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE users;
-- +goose StatementEnd
