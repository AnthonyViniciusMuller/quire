// Package clock is the wall clock adapter of the reading slice's time port.
//
// It truncates to the microsecond a timestamptz holds, so that an instant a use
// case stamped and the one a later read returns are the same value. Without it
// a test that writes and reads back compares two instants that differ by
// nanoseconds the database never stored.
//
// It is a wall clock, and every timestamp this slice stamps is not one. The
// replication timestamp of C01 has to be causally monotonic; what supplies that
// today is the per-record floor in crdt.Revision and crdt.Version, over the
// reading this adapter returns, and phase 9 replaces this adapter with the
// node-wide hybrid logical clock. The port does not change, and neither does
// any use case.
package clock

import (
	"time"

	"github.com/anthonyvsmuller/quire/internal/reading/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

// Service reads the machine's clock.
type Service struct{}

// Service satisfies the port the use cases hold.
var _ service.Clock = (*Service)(nil)

// New returns the adapter.
func New() *Service { return &Service{} }

// Now is the current instant, in UTC, at the resolution the database keeps.
func (*Service) Now() time.Time { return time.Now().UTC().Truncate(crdt.Resolution) }
