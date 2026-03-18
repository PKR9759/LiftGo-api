// internal/review/handler.go
package review

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/PKR9759/LiftGo-api/internal/auth"
	"github.com/PKR9759/LiftGo-api/internal/utils"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)

	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	review, err := h.service.Create(r.Context(), claims.UserID, req)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusCreated, review)
}

func (h *Handler) GetByReviewee(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	reviews, err := h.service.GetByReviewee(r.Context(), userID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, reviews)
}