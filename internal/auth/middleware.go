// internal/auth/middleware.go
package auth

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/PKR9759/LiftGo-api/internal/utils"
)

type contextKey string

const UserContextKey contextKey = "user"

func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("access_token")
		if err != nil {
			slog.Debug("missing access_token cookie")
			utils.WriteError(w, http.StatusUnauthorized, "missing access token")
			return
		}

		claims, err := ValidateToken(cookie.Value)
		if err != nil {
			utils.WriteError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func PopulateUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("access_token")
		if err == nil {
			claims, err := ValidateToken(cookie.Value)
			if err == nil {
				ctx := context.WithValue(r.Context(), UserContextKey, claims)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func GetUserFromContext(r *http.Request) *Claims {
	claims, _ := r.Context().Value(UserContextKey).(*Claims)
	return claims
}
