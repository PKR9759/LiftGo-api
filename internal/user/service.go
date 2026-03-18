// internal/user/service.go
package user

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

func (s *Service) GetMe(ctx context.Context, userID string) (*User, error) {
	u, err := s.repo.GetByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("user not found")
	}
	return u, err
}

func (s *Service) UpdateMe(ctx context.Context, userID string, req UpdateRequest) (*User, error) {
	if req.Role != "" {
		valid := map[string]bool{"rider": true, "driver": true, "both": true}
		if !valid[req.Role] {
			return nil, errors.New("role must be rider, driver or both")
		}
	}
	u, err := s.repo.Update(ctx, userID, req)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("user not found")
	}
	return u, err
}