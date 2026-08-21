package auth

import "context"

type contextKey struct{}

var userContextKey contextKey

// User is the authenticated caller after JIT provisioning in the local database.
type User struct {
	ID       int    `json:"userID"`
	Subject  string `json:"subject"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

func withUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

// FromContext returns the authenticated user bound by the auth middleware.
func FromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userContextKey).(User)
	return u, ok
}
