// internal/ride/service.go
package ride

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// haversineMeters returns the great-circle distance in meters between two lat/lng points.
func haversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusM = 6_371_000.0
	toRad := func(deg float64) float64 { return deg * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLng := toRad(lng2 - lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	return earthRadiusM * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func (s *Service) Create(ctx context.Context, driverID string, req CreateRequest) (*Ride, error) {
	if req.OriginLabel == "" || req.DestLabel == "" {
		return nil, errors.New("origin and destination labels are required")
	}
	if req.OriginLat == 0 || req.OriginLng == 0 {
		return nil, errors.New("origin coordinates are required")
	}
	if req.DestLat == 0 || req.DestLng == 0 {
		return nil, errors.New("destination coordinates are required")
	}
	if req.TotalSeats < 1 {
		return nil, errors.New("total seats must be at least 1")
	}
	if req.PricePerSeat < 0 {
		return nil, errors.New("price cannot be negative")
	}

	departure, err := time.Parse(time.RFC3339, req.DepartureAt)
	if err != nil {
		return nil, errors.New("invalid departure_at — use ISO 8601")
	}
	if departure.Before(time.Now()) {
		return nil, errors.New("departure time must be in the future")
	}

	// Validate waypoints if provided
	if len(req.Waypoints) > 0 {
		if len(req.Waypoints) < 2 {
			return nil, errors.New("waypoints must contain at least 2 points")
		}
		for i, wp := range req.Waypoints {
			if wp.Lat < -90 || wp.Lat > 90 {
				return nil, fmt.Errorf("waypoint %d: latitude must be between -90 and 90", i)
			}
			if wp.Lng < -180 || wp.Lng > 180 {
				return nil, fmt.Errorf("waypoint %d: longitude must be between -180 and 180", i)
			}
		}
		// First waypoint must be within 500m of origin
		first := req.Waypoints[0]
		if d := haversineMeters(first.Lat, first.Lng, req.OriginLat, req.OriginLng); d > 500 {
			return nil, errors.New("first waypoint must be near origin")
		}
		// Last waypoint must be within 500m of destination
		last := req.Waypoints[len(req.Waypoints)-1]
		if d := haversineMeters(last.Lat, last.Lng, req.DestLat, req.DestLng); d > 500 {
			return nil, errors.New("last waypoint must be near destination")
		}
	}

	return s.repo.Create(ctx, driverID, req)
}

func (s *Service) GetByID(ctx context.Context, id string) (*Ride, error) {
	ride, err := s.repo.GetByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("ride not found")
	}
	return ride, err
}

func (s *Service) FindNearby(ctx context.Context, p NearbyParams) ([]*Ride, error) {
	if p.OriginLat == 0 || p.OriginLng == 0 {
		return nil, errors.New("origin coordinates required")
	}
	if p.DestLat == 0 || p.DestLng == 0 {
		return nil, errors.New("destination coordinates required")
	}
	rides, err := s.repo.FindNearby(ctx, p)
	if err != nil {
		return nil, err
	}
	if rides == nil {
		rides = []*Ride{}
	}
	return rides, nil
}

func (s *Service) GetMyRides(ctx context.Context, driverID string) ([]*Ride, error) {
	rides, err := s.repo.GetByDriver(ctx, driverID)
	if err != nil {
		return nil, err
	}
	if rides == nil {
		rides = []*Ride{}
	}
	return rides, nil
}

func (s *Service) Update(ctx context.Context, id, driverID string, req UpdateRequest) (*Ride, error) {
	if req.DepartureAt != "" {
		departure, err := time.Parse(time.RFC3339, req.DepartureAt)
		if err != nil {
			return nil, errors.New("invalid departure_at — use ISO 8601")
		}
		if departure.Before(time.Now()) {
			return nil, errors.New("departure time must be in the future")
		}
	}
	ride, err := s.repo.Update(ctx, id, driverID, req)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("ride not found or you are not the driver")
	}
	return ride, err
}

func (s *Service) UpdateStatus(ctx context.Context, id, driverID, status string) error {
	if status != "active" && status != "completed" {
		return errors.New("invalid status: must be active or completed")
	}
	return s.repo.UpdateStatus(ctx, id, driverID, status)
}

func (s *Service) Cancel(ctx context.Context, id, driverID string) error {
	return s.repo.Cancel(ctx, id, driverID)
}
