package ports

import "time"

// Clock is the service's notion of "now". Use cases stamp events with it
// and RecordMeasurement uses it to reject a measurement dated in the future.
type Clock interface {
	Now() time.Time
}
