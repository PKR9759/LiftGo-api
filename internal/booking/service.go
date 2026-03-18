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
	return s.repo.Create(ctx, riderID, req)
}

func (s *Service) GetByID(ctx context.Context, id string) (*Booking, error) {
	return s.repo.GetByID(ctx, id)
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

func (s *Service) Confirm(ctx context.Context, id, driverID string) (*Booking, error) {
	return s.repo.UpdateStatus(ctx, id, driverID, "confirmed", "driver")
}

func (s *Service) Cancel(ctx context.Context, id, actorID, role string) (*Booking, error) {
	return s.repo.UpdateStatus(ctx, id, actorID, "cancelled", role)
}