// internal/booking/handler.go
package booking

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

	booking, err := h.service.Create(r.Context(), claims.UserID, req)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusCreated, booking)
}

func (h *Handler) GetMine(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)

	bookings, err := h.service.GetMine(r.Context(), claims.UserID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, bookings)
}

func (h *Handler) GetIncoming(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)

	bookings, err := h.service.GetIncoming(r.Context(), claims.UserID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, bookings)
}

func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	booking, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "booking not found")
		return
	}

	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	booking, err := h.service.Confirm(r.Context(), id, claims.UserID)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	booking, err := h.service.Cancel(r.Context(), id, claims.UserID, claims.Role)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, booking)
}