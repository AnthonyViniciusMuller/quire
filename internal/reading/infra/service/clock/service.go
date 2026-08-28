// Package clock is the reading slice's adapter of the time port, over the
// node's hybrid logical clock.
//
// Every instant this slice stamps is a replication timestamp and not a reading
// of the machine's clock. C01 in docs/tcc-corrections.md requires it to be
// causally monotonic: a causally later version of a record carrying an earlier
// instant is one bad edge, and one bad edge makes the last-writer-wins
// relation cyclic, which costs associativity and with it the eventual
// consistency of RNF03.
//
// What supplies that is [github.com/anthonyvsmuller/quire/internal/shared/hlc]
// over the whole node, and the per-record floor in crdt.Revision and
// crdt.Version over the row being written. The two compose, and the clock is
// shared with every other slice that stamps one: a second clock in this
// process would be a second answer to what "after" means here.
package clock

import (
	"time"

	"github.com/anthonyvsmuller/quire/internal/reading/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/hlc"
)

// Service stamps from the node's clock.
type Service struct {
	clock *hlc.Clock
}

// Service satisfies the port the use cases hold.
var _ service.Clock = (*Service)(nil)

// New returns the adapter over the node's clock.
func New(clock *hlc.Clock) *Service { return &Service{clock: clock} }

// Now is the instant to stamp the next write with, in UTC and at the
// resolution the database keeps.
func (s *Service) Now() time.Time { return s.clock.Now() }
