package uploads

import (
	"context"
	"time"
	"uuid"
)

// What the tests of this package reach in through, and nothing else does.
//
// The file is _test.go, so none of it is compiled into the node. It exists
// because the two things worth testing here are driven by time: a session
// expires after an interval a deployment sets, and the sweeper runs on a
// ticker. A test that waited for either would be a test that takes an hour, so
// it moves the clock and runs one pass instead.

// SetClock replaces the clock the deadlines are read from.
func (s *Service) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.now = now
}

// SweepOnce runs one pass of what [Service.Run] runs on a ticker.
func (s *Service) SweepOnce(ctx context.Context) { s.sweep(ctx) }

// SetBusy marks a session as having a call inside it, which is what the
// sweeper sees while a chunk is being written.
func (s *Service) SetBusy(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if held, open := s.sessions[id]; open {
		held.busy = true
	}
}

// ClearBusy is the other half, and is what the call itself does when it ends.
func (s *Service) ClearBusy(id uuid.UUID) { s.release(id) }
