// internal/auth/handler.go
package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"

	customMiddleware "github.com/PKR9759/LiftGo-api/internal/middleware"
	"github.com/PKR9759/LiftGo-api/internal/utils"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	slog.Info("Handler.Register entry", "request_id", reqID)

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("invalid request body", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.service.Register(r.Context(), req, reqID)
	if err != nil {
		slog.Error("registration failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("user registered successfully", "user_id", resp.User.ID, "email", resp.User.Email, "request_id", reqID)
	setAuthCookies(w, resp.AccessToken, resp.RefreshToken)
	utils.WriteJSON(w, http.StatusCreated, resp)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	slog.Info("Handler.Login entry", "request_id", reqID)

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("invalid request body", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.service.Login(r.Context(), req, reqID)
	if err != nil {
		slog.Error("login failed", "error", err, "email", req.Email, "request_id", reqID)
		utils.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	}

	slog.Info("user logged in successfully", "user_id", resp.User.ID, "email", resp.User.Email, "request_id", reqID)
	setAuthCookies(w, resp.AccessToken, resp.RefreshToken)
	utils.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	slog.Info("Handler.Refresh entry", "request_id", reqID)

	var validResp *AuthResponse
	var lastErr error
	var attemptCount int

	for _, c := range r.Cookies() {
		if c.Name == "refresh_token" {
			attemptCount++
			resp, err := h.service.RefreshSession(r.Context(), c.Value, reqID)
			if err == nil {
				validResp = resp
				break
			}
			lastErr = err
		}
	}

	if attemptCount == 0 {
		slog.Warn("refresh token cookie entirely missing", "request_id", reqID)
		clearAuthCookies(w)
		utils.WriteError(w, http.StatusUnauthorized, "missing refresh token")
		return
	}

	if validResp == nil {
		slog.Warn("refresh failed for all provided tokens", "error", lastErr, "request_id", reqID)
		clearAuthCookies(w)
		utils.WriteError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}

	setAuthCookies(w, validResp.AccessToken, validResp.RefreshToken)
	slog.Info("session refreshed successfully", "user_id", validResp.User.ID, "request_id", reqID)
	utils.WriteJSON(w, http.StatusOK, validResp)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	slog.Info("Handler.Logout entry", "request_id", reqID)

	cookie, err := r.Cookie("refresh_token")
	if err == nil {
		h.service.Logout(r.Context(), cookie.Value)
	}

	clearAuthCookies(w)
	slog.Info("user logged out", "request_id", reqID)
	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r)
	if claims == nil {
		utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]any{
		"user": map[string]string{
			"id":    claims.UserID,
			"email": claims.Email,
			"role":  claims.Role,
		},
	})
}

func setAuthCookies(w http.ResponseWriter, accessToken, refreshToken string) {
	secure := os.Getenv("COOKIE_SECURE") == "true"
	sameSite := parseSameSite(os.Getenv("COOKIE_SAMESITE"))
	domain := os.Getenv("COOKIE_DOMAIN")

	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    accessToken,
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
		Domain:   domain,
		Path:     "/",
		MaxAge:   900, // 15 min
	})

	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
		Domain:   domain,
		Path:     "/",
		MaxAge:   604800, // 7 days
	})
}

func parseSameSite(value string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none":
		return http.SameSiteNoneMode
	case "strict":
		return http.SameSiteStrictMode
	default:
		return http.SameSiteLaxMode
	}
}

func clearAuthCookies(w http.ResponseWriter) {
	domain := os.Getenv("COOKIE_DOMAIN")

	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    "",
		HttpOnly: true,
		Domain:   domain,
		Path:     "/",
		MaxAge:   -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		HttpOnly: true,
		Domain:   domain,
		Path:     "/",
		MaxAge:   -1,
	})
}
