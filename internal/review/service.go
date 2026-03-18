// internal/review/service.go
package review

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

func (s *Service) Create(ctx context.Context, reviewerID string, req CreateRequest) (*Review, error) {
	if req.BookingID == "" {
		return nil, errors.New("booking_id is required")
	}
	if req.RevieweeID == "" {
		return nil, errors.New("reviewee_id is required")
	}
	if req.Rating < 1 || req.Rating > 5 {
		return nil, errors.New("rating must be between 1 and 5")
	}
	return s.repo.Create(ctx, reviewerID, req)
}

func (s *Service) GetByReviewee(ctx context.Context, revieweeID string) ([]*Review, error) {
	reviews, err := s.repo.GetByReviewee(ctx, revieweeID)
	if err != nil {
		return nil, err
	}
	if reviews == nil {
		reviews = []*Review{}
	}
	return reviews, nil
}