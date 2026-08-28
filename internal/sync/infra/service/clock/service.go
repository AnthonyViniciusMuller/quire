// Package clock is the sync slice's adapter of the time port, over the node's
// hybrid logical clock.
//
// It is the adapter that carries both halves of C01 across the federation. What
// the other slices need from the clock is an instant to stamp with; this slice
// also has to tell it what it has been told — every operation it ingests
// carries an instant stamped somewhere else, and a local write made afterwards
// has to be stamped after it.
package clock

import (
	"time"

	"github.com/anthonyvsmuller/quire/internal/shared/hlc"
	"github.com/anthonyvsmuller/quire/internal/sync/application/service"
)

// Service stamps from, and reports to, the node's clock.
type Service struct {
	clock *hlc.Clock
}

// Service satisfies the port the use cases hold.
var _ service.Clock = (*Service)(nil)

// New returns the adapter over the node's clock.
func New(clock *hlc.Clock) *Service { return &Service{clock: clock} }

// Now is the instant to stamp the next write with.
func (s *Service) Now() time.Time { return s.clock.Now() }

// Observe folds an instant this node has been told about into the clock.
func (s *Service) Observe(at time.Time) bool { return s.clock.Observe(at) }
