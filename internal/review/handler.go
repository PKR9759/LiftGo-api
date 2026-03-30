// internal/review/handler.go
package review

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	customMiddleware "github.com/PKR9759/LiftGo-api/internal/middleware"
	"github.com/PKR9759/LiftGo-api/internal/utils"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)

	slog.Info("Handler.Create review entry", "request_id", reqID, "reviewer_id", claims.UserID)

	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("invalid request body", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	review, err := h.service.Create(r.Context(), claims.UserID, req)
	if err != nil {
		slog.Error("Create review failed", "error", err, "request_id", reqID, "reviewer_id", claims.UserID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("review created successfully", "review_id", review.ID, "request_id", reqID)
	utils.WriteJSON(w, http.StatusCreated, review)
}

func (h *Handler) GetByReviewee(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	userID := chi.URLParam(r, "id")

	slog.Info("Handler.GetByReviewee entry", "request_id", reqID, "reviewee_id", userID)

	reviews, err := h.service.GetByReviewee(r.Context(), userID)
	if err != nil {
		slog.Error("GetByReviewee failed", "error", err, "request_id", reqID, "reviewee_id", userID)
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, reviews)
}
