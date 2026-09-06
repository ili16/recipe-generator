package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc"
	"github.com/golang-jwt/jwt/v4"
)

// Provisioner performs the just-in-time user upsert keyed by the token subject claim.
type Provisioner interface {
	EnsureUser(ctx context.Context, subject, email, name, provider string) (int, error)
}

type Config struct {
	KeycloakURL  string
	ClientID     string
	ClientSecret string
	Audience     string
	Introspect   bool
}

// ConfigFromEnv reads auth configuration from the process environment.
func ConfigFromEnv() Config {
	clientID := os.Getenv("KEYCLOAK_BACKEND_CLIENT_ID")
	if clientID == "" {
		clientID = "backend"
	}
	secret := os.Getenv("KEYCLOAK_CLIENT_SECRET")
	return Config{
		KeycloakURL:  strings.TrimSuffix(os.Getenv("KEYCLOAK_URL"), "/"),
		ClientID:     clientID,
		ClientSecret: secret,
		Audience:     os.Getenv("KEYCLOAK_AUDIENCE"),
		Introspect:   secret != "" && os.Getenv("AUTH_INTROSPECT") != "false",
	}
}

type Authenticator struct {
	cfg         Config
	jwks        *keyfunc.JWKS
	provisioner Provisioner
	client      *http.Client
}

func New(cfg Config, provisioner Provisioner) (*Authenticator, error) {
	if cfg.KeycloakURL == "" {
		return nil, errors.New("KEYCLOAK_URL is required")
	}
	jwks, err := keyfunc.Get(cfg.KeycloakURL+"/protocol/openid-connect/certs", keyfunc.Options{
		RefreshInterval: time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	return &Authenticator{
		cfg:         cfg,
		jwks:        jwks,
		provisioner: provisioner,
		client:      &http.Client{Timeout: 5 * time.Second},
	}, nil
}

// Require validates the bearer token, provisions the user on first sight, and binds it to the request context.
func (a *Authenticator) Require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			unauthorized(w, "missing bearer token")
			return
		}

		claims, err := a.parse(raw)
		if err != nil {
			slog.Warn("token rejected", "error", err)
			unauthorized(w, "invalid token")
			return
		}

		if a.cfg.Introspect {
			active, err := a.introspect(r.Context(), raw)
			if err != nil {
				slog.Error("introspection failed", "error", err)
				unauthorized(w, "token introspection failed")
				return
			}
			if !active {
				unauthorized(w, "inactive token")
				return
			}
		}

		subject := claims.stringClaim("sub")
		if subject == "" {
			unauthorized(w, "token is missing sub claim")
			return
		}

		slog.Debug("auth: token validated", "subject", subject, "introspect", a.cfg.Introspect)

		user := User{
			Subject:  subject,
			Email:    claims.stringClaim("email"),
			Name:     claims.displayName(),
			Provider: claims.provider(),
			IsAdmin:  claims.isAdmin(),
		}

		id, err := a.provisioner.EnsureUser(r.Context(), user.Subject, user.Email, user.Name, user.Provider)
		if err != nil {
			slog.Error("user provisioning failed", "subject", user.Subject, "error", err)
			http.Error(w, "failed to provision user", http.StatusInternalServerError)
			return
		}
		user.ID = id
		slog.Debug("auth: user provisioned", "userID", id, "subject", user.Subject)

		next(w, r.WithContext(withUser(r.Context(), user)))
	}
}

// RequireAdmin behaves like Require, additionally rejecting non-admin users with 403.
func (a *Authenticator) RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return a.Require(func(w http.ResponseWriter, r *http.Request) {
		user, _ := FromContext(r.Context())
		if !user.IsAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

type mapClaims jwt.MapClaims

func (a *Authenticator) parse(raw string) (mapClaims, error) {
	token, err := jwt.Parse(raw, a.jwks.Keyfunc,
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512"}))
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("token is not valid")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("unexpected claims type")
	}

	if a.cfg.KeycloakURL != "" && !claims.VerifyIssuer(a.cfg.KeycloakURL, true) {
		return nil, errors.New("issuer mismatch")
	}
	if a.cfg.Audience != "" && !claims.VerifyAudience(a.cfg.Audience, true) {
		return nil, errors.New("audience mismatch")
	}

	return mapClaims(claims), nil
}

func (a *Authenticator) introspect(ctx context.Context, token string) (bool, error) {
	slog.Debug("auth: introspecting token")
	t := time.Now()
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.cfg.KeycloakURL+"/protocol/openid-connect/token/introspect",
		strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	req.SetBasicAuth(a.cfg.ClientID, a.cfg.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("introspection endpoint returned status %d", resp.StatusCode)
	}

	var body struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, err
	}
	slog.Debug("auth: introspection done", "active", body.Active, "dur_ms", time.Since(t).Milliseconds())
	return body.Active, nil
}

func (c mapClaims) stringClaim(key string) string {
	v, ok := c[key].(string)
	if !ok {
		return ""
	}
	return v
}

func (c mapClaims) displayName() string {
	for _, key := range []string{"name", "preferred_username", "email"} {
		if v := c.stringClaim(key); v != "" {
			return v
		}
	}
	return ""
}

func (c mapClaims) provider() string {
	if v := c.stringClaim("identity_provider"); v != "" {
		return v
	}
	return "keycloak"
}

// isAdmin reports whether the token's realm_access.roles includes "admin".
func (c mapClaims) isAdmin() bool {
	realmAccess, ok := c["realm_access"].(map[string]any)
	if !ok {
		return false
	}
	roles, ok := realmAccess["roles"].([]any)
	if !ok {
		return false
	}
	for _, role := range roles {
		if s, ok := role.(string); ok && s == "admin" {
			return true
		}
	}
	return false
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) < 7 || !strings.EqualFold(auth[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(auth[7:])
}

func unauthorized(w http.ResponseWriter, msg string) {
	http.Error(w, msg, http.StatusUnauthorized)
}

// Optional enriches the context with the authenticated user if a valid token is present, but never rejects the request.
func (a *Authenticator) Optional(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			next(w, r)
			return
		}
		claims, err := a.parse(raw)
		if err != nil {
			next(w, r)
			return
		}
		if a.cfg.Introspect {
			active, err := a.introspect(r.Context(), raw)
			if err != nil || !active {
				next(w, r)
				return
			}
		}
		subject := claims.stringClaim("sub")
		if subject == "" {
			next(w, r)
			return
		}
		user := User{
			Subject:  subject,
			Email:    claims.stringClaim("email"),
			Name:     claims.displayName(),
			Provider: claims.provider(),
			IsAdmin:  claims.isAdmin(),
		}
		id, err := a.provisioner.EnsureUser(r.Context(), user.Subject, user.Email, user.Name, user.Provider)
		if err != nil {
			slog.Warn("auth: optional provision failed, continuing as anonymous", "error", err)
			next(w, r)
			return
		}
		user.ID = id
		next(w, r.WithContext(withUser(r.Context(), user)))
	}
}
