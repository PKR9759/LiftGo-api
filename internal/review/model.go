// internal/review/model.go
package review

import "time"

type Review struct {
	ID           string    `json:"id"`
	BookingID    string    `json:"booking_id"`
	ReviewerID   string    `json:"reviewer_id"`
	ReviewerName string    `json:"reviewer_name"`
	RevieweeID   string    `json:"reviewee_id"`
	Rating       int       `json:"rating"`
	Comment      *string   `json:"comment,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type CreateRequest struct {
	BookingID  string `json:"booking_id"`
	RevieweeID string `json:"reviewee_id"`
	Rating     int    `json:"rating"`
	Comment    string `json:"comment"`
}