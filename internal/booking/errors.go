package booking

import "errors"

var (
	ErrInvalidSegmentOrder = errors.New("pickup must come before dropoff along this route")
	ErrPointNotOnRoute     = errors.New("pickup and dropoff must both be near the route")
	ErrSegmentCapacity     = errors.New("not enough seats available for this route segment")
)
