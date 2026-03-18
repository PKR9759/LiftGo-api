// internal/seek/service.go
package seek

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, seekerID string, req CreateRequest) (*Seek, error) {
	if req.OriginLabel == "" || req.DestLabel == "" {
		return nil, errors.New("origin and destination labels are required")
	}
	if req.OriginLat == 0 || req.OriginLng == 0 {
		return nil, errors.New("origin coordinates are required")
	}
	if req.DestLat == 0 || req.DestLng == 0 {
		return nil, errors.New("destination coordinates are required")
	}
	if req.SeatsNeeded < 1 {
		req.SeatsNeeded = 1
	}
	return s.repo.Create(ctx, seekerID, req)
}

func (s *Service) GetByID(ctx context.Context, id string) (*Seek, error) {
	seek, err := s.repo.GetByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("seek not found")
	}
	return seek, err
}

func (s *Service) GetMine(ctx context.Context, seekerID string) ([]*Seek, error) {
	seeks, err := s.repo.GetBySeeker(ctx, seekerID)
	if err != nil {
		return nil, err
	}
	if seeks == nil {
		seeks = []*Seek{}
	}
	return seeks, nil
}

func (s *Service) FindNearby(ctx context.Context, p NearbyParams) ([]*Seek, error) {
	if p.OriginLat == 0 || p.OriginLng == 0 {
		return nil, errors.New("origin coordinates required")
	}
	if p.DestLat == 0 || p.DestLng == 0 {
		return nil, errors.New("destination coordinates required")
	}

	// expire stale seeks before returning results
	_ = s.repo.ExpireStale(ctx)

	seeks, err := s.repo.FindNearby(ctx, p)
	if err != nil {
		return nil, err
	}
	if seeks == nil {
		seeks = []*Seek{}
	}
	return seeks, nil
}

func (s *Service) Cancel(ctx context.Context, id, seekerID string) error {
	return s.repo.Cancel(ctx, id, seekerID)
}