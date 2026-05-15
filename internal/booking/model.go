// internal/booking/model.go
package booking

import "time"

type Booking struct {
	ID              string     `json:"id"`
	RideID          string     `json:"ride_id"`
	RiderID         string     `json:"rider_id"`
	RiderName       string     `json:"rider_name"`
	DriverID        string     `json:"driver_id"`
	DriverName      string     `json:"driver_name"`
	OriginLabel     string     `json:"origin_label"`
	DestLabel       string     `json:"dest_label"`
	DepartureAt     time.Time  `json:"departure_at"`
	Seats           int        `json:"seats"`
	Status          string     `json:"status"`
	RideStatus      string     `json:"ride_status"`
	TotalPrice      float64    `json:"total_price"`
	PickedUpAt      *time.Time `json:"picked_up_at,omitempty"`
	DroppedAt       *time.Time `json:"dropped_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	RiderReadyLat   *float64   `json:"rider_ready_lat,omitempty"`
	RiderReadyLng   *float64   `json:"rider_ready_lng,omitempty"`
	PickupFraction  float64    `json:"pickup_fraction"`
	DropoffFraction float64    `json:"dropoff_fraction"`
	PickupLat       *float64   `json:"pickup_lat,omitempty"`
	PickupLng       *float64   `json:"pickup_lng,omitempty"`
	DropoffLat      *float64   `json:"dropoff_lat,omitempty"`
	DropoffLng      *float64   `json:"dropoff_lng,omitempty"`
	IdempotencyKey  string     `json:"idempotency_key,omitempty"`

	// Segment fare breakdown
	FullRoutePricePerSeat float64 `json:"full_route_price_per_seat"`
	SegmentPricePerSeat   float64 `json:"segment_price_per_seat"`
	SegmentCoveragePct    float64 `json:"segment_coverage_pct"`
	TotalFullPrice        float64 `json:"total_full_price"`
	TotalSavings          float64 `json:"total_savings"`
}

type BookingWithRiderInfo struct {
	Booking
	RiderRating    float64 `json:"rider_rating"`
	RiderOriginLat float64 `json:"rider_origin_lat"`
	RiderOriginLng float64 `json:"rider_origin_lng"`
	RiderDestLat   float64 `json:"rider_dest_lat"`
	RiderDestLng   float64 `json:"rider_dest_lng"`
}

type CreateRequest struct {
	IdempotencyKey string  `json:"idempotency_key"`
	RideID     string  `json:"ride_id"`
	Seats      int     `json:"seats"`
	PickupLat  float64 `json:"pickup_lat"`
	PickupLng  float64 `json:"pickup_lng"`
	DropoffLat float64 `json:"dropoff_lat"`
	DropoffLng float64 `json:"dropoff_lng"`
}

type PickedUpRequest struct {
	DriverLat float64 `json:"driver_lat"`
	DriverLng float64 `json:"driver_lng"`
}
