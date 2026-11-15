package auth

import (
	"context"
	"net/http"
)

type contextKey string

const userContextKey contextKey = "user"

// WithUser creates a new context containing the User struct.
func WithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// LookupUser retrieves the User from the context and reports whether a user
// (including an explicit Guest) was attached to the context.
func LookupUser(ctx context.Context) (User, bool) {
	if ctx == nil {
		return Guest(), false
	}
	val := ctx.Value(userContextKey)
	if user, ok := val.(User); ok {
		return user, true
	}
	return Guest(), false
}

// GetUser retrieves the User from the context.
// If no user is found, it returns a safe Guest user.
func GetUser(ctx context.Context) User {
	user, _ := LookupUser(ctx)
	return user
}

// GetUserFromRequest is a helper for handlers.
func GetUserFromRequest(r *http.Request) User {
	return GetUser(r.Context())
}
