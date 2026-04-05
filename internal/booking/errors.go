package booking

import "errors"

var (
	ErrInvalidSegmentOrder = errors.New("pickup must come before dropoff along this route")
	ErrSegmentCapacity     = errors.New("not enough seats available for this route segment")
)
