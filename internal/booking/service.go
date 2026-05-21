// internal/booking/service.go
package booking

import (
	"context"
	"errors"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, riderID string, req CreateRequest) (*Booking, error) {
	if req.RideID == "" {
		return nil, errors.New("ride_id is required")
	}
	if req.Seats < 1 {
		return nil, errors.New("seats must be at least 1")
	}
	if req.PickupLat == 0 && req.PickupLng == 0 {
		return nil, errors.New("pickup_lat and pickup_lng are required")
	}
	if req.DropoffLat == 0 && req.DropoffLng == 0 {
		return nil, errors.New("dropoff_lat and dropoff_lng are required")
	}
	if req.PickupLat < -90 || req.PickupLat > 90 || req.DropoffLat < -90 || req.DropoffLat > 90 {
		return nil, errors.New("pickup_lat and dropoff_lat must be between -90 and 90")
	}
	if req.PickupLng < -180 || req.PickupLng > 180 || req.DropoffLng < -180 || req.DropoffLng > 180 {
		return nil, errors.New("pickup_lng and dropoff_lng must be between -180 and 180")
	}
	return s.repo.Create(ctx, riderID, req)
}

func (s *Service) GetByID(ctx context.Context, id string) (*Booking, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) GetByIDForUser(ctx context.Context, id, userID string) (*Booking, error) {
	return s.repo.GetByIDForUser(ctx, id, userID)
}

func (s *Service) GetMine(ctx context.Context, riderID string) ([]*Booking, error) {
	bookings, err := s.repo.GetByRider(ctx, riderID)
	if err != nil {
		return nil, err
	}
	if bookings == nil {
		bookings = []*Booking{}
	}
	return bookings, nil
}

func (s *Service) GetIncoming(ctx context.Context, driverID string) ([]*Booking, error) {
	bookings, err := s.repo.GetIncoming(ctx, driverID)
	if err != nil {
		return nil, err
	}
	if bookings == nil {
		bookings = []*Booking{}
	}
	return bookings, nil
}

func (s *Service) Confirm(ctx context.Context, id, driverID string) (*Booking, []*Booking, error) {
	b, err := s.repo.ConfirmBooking(ctx, id, driverID)
	if err != nil {
		return nil, nil, err
	}

	var autoCancelled []*Booking
	if b.RideStatus == "full" {
		autoCancelled, err = s.repo.CancelPendingBookingsOnRide(ctx, b.RideID, b.ID)
		if err != nil {
			return nil, nil, err
		}
	}

	return b, autoCancelled, nil
}

func (s *Service) Cancel(ctx context.Context, id, actorID, role string) (*Booking, error) {
	return s.repo.CancelBooking(ctx, id, actorID, role)
}

func (s *Service) GetRideBookingsWithRiderInfo(ctx context.Context, rideID, driverID string) ([]*BookingWithRiderInfo, error) {
	list, err := s.repo.GetRideBookingsWithRiderInfo(ctx, rideID, driverID)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []*BookingWithRiderInfo{}
	}
	return list, nil
}

func (s *Service) MarkRiderReady(ctx context.Context, id, riderID string, lat, lng *float64) (*Booking, error) {
	return s.repo.MarkRiderReady(ctx, id, riderID, lat, lng)
}

func (s *Service) MarkPickedUp(ctx context.Context, id, driverID string) (*Booking, error) {
	return s.repo.MarkPickedUp(ctx, id, driverID)
}

// MarkDropped returns the updated booking, and a boolean indicating if the entire ride is now completed
func (s *Service) MarkDropped(ctx context.Context, id, driverID string) (*Booking, bool, error) {
	b, err := s.repo.MarkDropped(ctx, id, driverID)
	if err != nil {
		return nil, false, err
	}
	completed, err := s.repo.CheckAndUpdateRideCompletion(ctx, b.RideID)
	if err != nil {
		return nil, false, err // atomic status check failed
	}
	return b, completed, nil
}

// MarkNoShow returns the updated booking, and a boolean indicating if the entire ride is now completed
func (s *Service) MarkNoShow(ctx context.Context, id, driverID string) (*Booking, bool, error) {
	b, err := s.repo.MarkNoShow(ctx, id, driverID)
	if err != nil {
		return nil, false, err
	}
	completed, err := s.repo.CheckAndUpdateRideCompletion(ctx, b.RideID)
	if err != nil {
		return nil, false, err // atomic status check failed
	}
	return b, completed, nil
}

func (s *Service) CheckDriverLocation(ctx context.Context, bookingID string, lat, lng float64) (bool, error) {
	return s.repo.CheckDriverLocation(ctx, bookingID, lat, lng)
}

func (s *Service) CheckRiderLocation(ctx context.Context, bookingID string, lat, lng float64) (bool, error) {
	return s.repo.CheckRiderLocation(ctx, bookingID, lat, lng)
}
