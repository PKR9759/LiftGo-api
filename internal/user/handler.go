// internal/user/handler.go
package user

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	customMiddleware "github.com/PKR9759/LiftGo-api/internal/middleware"
	"github.com/PKR9759/LiftGo-api/internal/utils"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	slog.Info("Handler.GetMe entry", "request_id", reqID)

	claims := auth.GetUserFromContext(r)
	if claims == nil {
		slog.Warn("unauthorized access attempt", "request_id", reqID)
		utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	slog.Info("getting profile for user", "user_id", claims.UserID, "request_id", reqID)

	user, err := h.service.GetMe(r.Context(), claims.UserID)
	if err != nil {
		slog.Error("GetMe failed", "error", err, "user_id", claims.UserID, "request_id", reqID)
		utils.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, user)
}

func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	slog.Info("Handler.UpdateMe entry", "request_id", reqID)

	claims := auth.GetUserFromContext(r)
	if claims == nil {
		slog.Warn("unauthorized access attempt", "request_id", reqID)
		utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("invalid request body", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := h.service.UpdateMe(r.Context(), claims.UserID, req)
	if err != nil {
		slog.Error("UpdateMe failed", "error", err, "user_id", claims.UserID, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("user profile updated successfully", "user_id", claims.UserID, "request_id", reqID)
	utils.WriteJSON(w, http.StatusOK, user)
}
