// internal/user/model.go
package user

import "time"

type User struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	Phone        *string   `json:"phone,omitempty"`
	AvatarURL    *string   `json:"avatar_url,omitempty"`
	AvgRating    float64   `json:"avg_rating"`
	TotalReviews int       `json:"total_reviews"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type UpdateRequest struct {
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	AvatarURL string `json:"avatar_url"`
	Role      string `json:"role"`
}