package storage

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Users implements just-in-time user provisioning keyed by the OIDC subject claim.
type Users struct{}

func (Users) EnsureUser(ctx context.Context, subject, email, name, provider string) (int, error) {
	const query = `
		INSERT INTO users (oauth_id, name, email, oauth_provider, last_seen_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (oauth_id) DO UPDATE
		SET name           = COALESCE(NULLIF(EXCLUDED.name, ''), users.name),
		    email          = COALESCE(NULLIF(EXCLUDED.email, ''), users.email),
		    oauth_provider = COALESCE(NULLIF(EXCLUDED.oauth_provider, ''), users.oauth_provider),
		    last_seen_at   = NOW()
		RETURNING id`

	slog.Debug("db: ensure user", "subject", subject, "provider", provider)
	t := time.Now()
	var userID int
	if err := Pool.QueryRow(ctx, query, subject, name, email, provider).Scan(&userID); err != nil {
		return 0, fmt.Errorf("ensure user %s: %w", subject, err)
	}
	slog.Debug("db: ensure user done", "userID", userID, "dur_ms", time.Since(t).Milliseconds())
	return userID, nil
}
