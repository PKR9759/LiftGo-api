// internal/auth/handler.go
package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"

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
	utils.WriteJSON(w, http.StatusOK, resp)
}
