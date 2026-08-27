// Package clock is the wall clock implementation of the time port.
package clock

import (
	"time"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
)

// resolution is what a timestamptz column keeps. Rounding here rather than
// letting the driver do it on the way in is what makes a record read back
// equal to the one still in memory: without it, an entity written with
// nanoseconds and then fetched compares unequal to itself, which is a test
// that fails for a reason nobody would guess.
const resolution = time.Microsecond

// Service reads the system clock.
type Service struct{}

// Service satisfies the port the use cases hold.
var _ service.Clock = (*Service)(nil)

// New returns the clock.
func New() *Service { return &Service{} }

// Now is the current instant, in UTC and at the resolution the database keeps.
//
// UTC rather than the process's local zone, because the instant travels: a
// discovery time is rendered into a protobuf timestamp and read on devices in
// other zones, and a node's catalogue is compared against a peer's.
func (s *Service) Now() time.Time { return time.Now().UTC().Truncate(resolution) }
