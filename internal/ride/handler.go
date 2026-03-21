// internal/ride/handler.go
package ride

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	"github.com/PKR9759/LiftGo-api/internal/notification"
	"github.com/PKR9759/LiftGo-api/internal/utils"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	service     *Service
	db          *pgxpool.Pool
	emailClient *notification.EmailClient
}

func NewHandler(service *Service, db *pgxpool.Pool, emailClient *notification.EmailClient) *Handler {
	return &Handler{
		service:     service,
		db:          db,
		emailClient: emailClient,
	}
}

// GET /api/rides/nearby?origin_lat=&origin_lng=&dest_lat=&dest_lng=&radius=
func (h *Handler) FindNearby(w http.ResponseWriter, r *http.Request) {
	originLat, _ := strconv.ParseFloat(r.URL.Query().Get("origin_lat"), 64)
	originLng, _ := strconv.ParseFloat(r.URL.Query().Get("origin_lng"), 64)
	destLat, _ := strconv.ParseFloat(r.URL.Query().Get("dest_lat"), 64)
	destLng, _ := strconv.ParseFloat(r.URL.Query().Get("dest_lng"), 64)
	radius, _ := strconv.ParseFloat(r.URL.Query().Get("radius"), 64)

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

// PUT /api/rides/:id/status
func (h *Handler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	var req UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.service.UpdateStatus(r.Context(), id, claims.UserID, req.Status); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	go func() {
		if req.Status != "active" && req.Status != "completed" {
			return
		}

		ctx := context.Background()

		var driverName, driverEmail string
		err := h.db.QueryRow(ctx, "SELECT name, email FROM users WHERE id = $1", claims.UserID).Scan(&driverName, &driverEmail)
		if err != nil {
			log.Printf("Failed to fetch driver details for ride notification: %v", err)
			return
		}

		rows, err := h.db.Query(ctx,
			`SELECT u.email, u.name
			 FROM bookings b
			 JOIN users u ON u.id = b.rider_id
			 WHERE b.ride_id = $1 AND b.status = 'confirmed'`,
			id,
		)
		if err != nil {
			log.Printf("Failed to query confirmed bookings for ride notification: %v", err)
			return
		}
		defer rows.Close()

		type RiderInfo struct {
			Email string
			Name  string
		}
		var riders []RiderInfo
		for rows.Next() {
			var ri RiderInfo
			if err := rows.Scan(&ri.Email, &ri.Name); err == nil {
				riders = append(riders, ri)
			}
		}

		if req.Status == "active" {
			for _, ri := range riders {
				h.emailClient.SendDriverStartedRideToRider(ri.Email, ri.Name, driverName)
			}
		} else if req.Status == "completed" {
			for _, ri := range riders {
				h.emailClient.SendRideCompletedToRider(ri.Email, ri.Name, driverName)
				h.emailClient.SendRideCompletedToDriver(driverEmail, driverName, ri.Name)
			}
		}
	}()

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "status updated to " + req.Status})
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
