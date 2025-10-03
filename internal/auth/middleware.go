package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type contextKey string

const claimsContextKey contextKey = "funpot-auth-claims"

// Middleware validates bearer tokens and injects claims into the request context.
func Middleware(issuer *JWTIssuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authorization := r.Header.Get("Authorization")
			if authorization == "" {
				writeAuthError(w, http.StatusUnauthorized, "missing authorization header")
				return
			}

			parts := strings.SplitN(authorization, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				writeAuthError(w, http.StatusUnauthorized, "invalid authorization header")
				return
			}

			claims, err := issuer.Parse(parts[1])
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid token")
				return
			}

			ctx := context.WithValue(r.Context(), claimsContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromContext extracts auth claims from the context.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(*Claims)
	return claims, ok
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":     message,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}
