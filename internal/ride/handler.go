// internal/ride/handler.go
package ride

import (
	"encoding/json"
	"net/http"
	"strconv"

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

// GET /api/rides/nearby?origin_lat=&origin_lng=&dest_lat=&dest_lng=&radius=
func (h *Handler) FindNearby(w http.ResponseWriter, r *http.Request) {
	originLat, _ := strconv.ParseFloat(r.URL.Query().Get("origin_lat"), 64)
	originLng, _ := strconv.ParseFloat(r.URL.Query().Get("origin_lng"), 64)
	destLat, _   := strconv.ParseFloat(r.URL.Query().Get("dest_lat"),   64)
	destLng, _   := strconv.ParseFloat(r.URL.Query().Get("dest_lng"),   64)
	radius, _    := strconv.ParseFloat(r.URL.Query().Get("radius"),     64)

	rides, err := h.service.FindNearby(r.Context(), NearbyParams{
		OriginLat:    originLat,
		OriginLng:    originLng,
		DestLat:      destLat,
		DestLng:      destLng,
		RadiusMeters: radius,
	})
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, rides)
}

// GET /api/rides/:id
func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ride, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, ride)
}

// POST /api/rides
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)

	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ride, err := h.service.Create(r.Context(), claims.UserID, req)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusCreated, ride)
}

// GET /api/rides/mine
func (h *Handler) GetMine(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)

	rides, err := h.service.GetMyRides(r.Context(), claims.UserID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, rides)
}

// PUT /api/rides/:id
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	var req UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ride, err := h.service.Update(r.Context(), id, claims.UserID, req)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, ride)
}

// DELETE /api/rides/:id
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	if err := h.service.Cancel(r.Context(), id, claims.UserID); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "ride cancelled"})
}