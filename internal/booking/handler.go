// internal/booking/handler.go
package booking

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

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

	go func() {
		var driverEmail string
		err := h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", booking.DriverID).Scan(&driverEmail)
		if err != nil {
			log.Printf("Failed to fetch driver email for new booking email: %v", err)
			return
		}
		h.emailClient.SendNewBookingRequestToDriver(
			driverEmail,
			booking.DriverName,
			booking.RiderName,
			booking.OriginLabel,
			booking.DestLabel,
			booking.DepartureAt,
			booking.Seats,
		)
	}()

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

	go func() {
		var riderEmail string
		err := h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", booking.RiderID).Scan(&riderEmail)
		if err != nil {
			log.Printf("Failed to fetch rider email for booking confirmed email: %v", err)
			return
		}
		h.emailClient.SendBookingConfirmedToRider(
			riderEmail,
			booking.RiderName,
			booking.DriverName,
			booking.OriginLabel,
			booking.DestLabel,
			booking.DepartureAt,
		)
	}()

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

	go func() {
		isDriverCancelling := (claims.UserID == booking.DriverID)

		recipientID := booking.DriverID
		recipientName := booking.DriverName
		cancelledByName := booking.RiderName

		if isDriverCancelling {
			recipientID = booking.RiderID
			recipientName = booking.RiderName
			cancelledByName = booking.DriverName
		}

		var recipientEmail string
		err := h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", recipientID).Scan(&recipientEmail)
		if err != nil {
			log.Printf("Failed to fetch recipient email for booking cancelled email: %v", err)
			return
		}

		h.emailClient.SendBookingCancelled(
			recipientEmail,
			recipientName,
			cancelledByName,
			booking.OriginLabel,
			booking.DestLabel,
		)
	}()

	utils.WriteJSON(w, http.StatusOK, booking)
}
