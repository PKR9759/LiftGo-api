// internal/seek/handler.go
package seek

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

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

// GET /api/seeks/nearby?origin_lat=&origin_lng=&dest_lat=&dest_lng=&radius=
func (h *Handler) FindNearby(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	slog.Info("Handler.FindNearby seeks entry", "request_id", reqID)

	originLat, _ := strconv.ParseFloat(r.URL.Query().Get("origin_lat"), 64)
	originLng, _ := strconv.ParseFloat(r.URL.Query().Get("origin_lng"), 64)
	destLat, _ := strconv.ParseFloat(r.URL.Query().Get("dest_lat"), 64)
	destLng, _ := strconv.ParseFloat(r.URL.Query().Get("dest_lng"), 64)
	radius, _ := strconv.ParseFloat(r.URL.Query().Get("radius"), 64)

	var excludeUserID string
	if claims := auth.GetUserFromContext(r); claims != nil {
		excludeUserID = claims.UserID
	}

	seeks, err := h.service.FindNearby(r.Context(), NearbyParams{
		OriginLat:     originLat,
		OriginLng:     originLng,
		DestLat:       destLat,
		DestLng:       destLng,
		RadiusMeters:  radius,
		ExcludeUserID: excludeUserID,
	})
	if err != nil {
		slog.Error("FindNearby seeks failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, seeks)
}

// GET /api/seeks/:id
func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.GetByID seek entry", "request_id", reqID, "seek_id", id)

	seek, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		slog.Error("GetByID seek failed", "error", err, "seek_id", id, "request_id", reqID)
		utils.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, seek)
}

// POST /api/seeks
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)

	slog.Info("Handler.Create seek entry", "request_id", reqID, "user_id", claims.UserID)

	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("invalid request body", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	seek, err := h.service.Create(r.Context(), claims.UserID, req)
	if err != nil {
		slog.Error("Create seek failed", "error", err, "user_id", claims.UserID, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("seek created successfully", "seek_id", seek.ID, "user_id", claims.UserID, "request_id", reqID)
	utils.WriteJSON(w, http.StatusCreated, seek)
}

// GET /api/seeks/mine
func (h *Handler) GetMine(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)

	slog.Info("Handler.GetMine seeks entry", "request_id", reqID, "user_id", claims.UserID)

	seeks, err := h.service.GetMine(r.Context(), claims.UserID)
	if err != nil {
		slog.Error("GetMine seeks failed", "error", err, "user_id", claims.UserID, "request_id", reqID)
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, seeks)
}

// DELETE /api/seeks/:id
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.Cancel seek entry", "request_id", reqID, "seek_id", id, "user_id", claims.UserID)

	if err := h.service.Cancel(r.Context(), id, claims.UserID); err != nil {
		slog.Error("Cancel seek failed", "error", err, "seek_id", id, "user_id", claims.UserID, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("seek cancelled successfully", "seek_id", id, "user_id", claims.UserID, "request_id", reqID)
	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "seek cancelled"})
}
