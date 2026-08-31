// Package uploads holds the files of a chunked upload between the calls that
// send them.
//
// It is the state D11 records the cost of. A streamed upload is one call and
// holds its bytes for as long as that call runs; a chunked one is three or
// more, and something has to remember the half-received file in between. That
// something is this, and it is in the process rather than in PostgreSQL or in
// the object store for a reason that is not a preference: staging holds the
// bytes in a file it unlinks the moment it opens it, so there is no name any
// other process — or any other replica — could reopen them by.
//
// The node already runs a single replica, because the replication worker of
// the sync slice ticks per process. This is a second reason for that, and the
// deployment says so beside the first.
//
// # What bounds it
//
// Two things, and both are about a caller that stops rather than one that
// misbehaves. A session nobody has sent to for the configured while is ended by
// [Service.Run], which is what covers the client that closed its laptop instead
// of its upload. And a reader may hold only so many at once, because each one
// is a descriptor and a file's worth of disk, and a caller that opened
// thousands would spend the node's disk without ever sending a byte.
package uploads

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opBegin   = "library/uploads: begin"
	opAppend  = "library/uploads: append"
	opFinish  = "library/uploads: finish"
	opDiscard = "library/uploads: discard"
)

// perReader is how many sessions one reader may hold open at once.
//
// It is small because a session is not a queue position: it is an open
// descriptor and up to a whole file of disk, and a reader importing their
// library sends the files one after another rather than all at once. A client
// that wants more parallelism than this is a client the node would rather slow
// down than run out of disk for.
const perReader = 4

// sweepEvery is how often [Service.Run] looks for sessions nobody is sending
// to.
//
// It is not the expiry, which is configuration: it is how coarsely the expiry
// is enforced, and a session outliving its deadline by up to this long costs
// the node the disk it was already holding.
const sweepEvery = time.Minute

// Service holds the sessions of one node.
type Service struct {
	staging service.Staging
	// expireAfter is how long a session may go without a chunk before the
	// sweeper ends it.
	expireAfter time.Duration
	// limit is the largest file the node accepts, which bounds each session's
	// staging holder.
	limit  int64
	logger *slog.Logger

	mu       sync.Mutex
	sessions map[uuid.UUID]*session
	// held is how many sessions each reader has open, kept beside the map so
	// that the per-reader bound does not cost a walk of every session in the
	// node.
	held map[uuid.UUID]int
	// now is the clock the deadlines are read from, so that a test can expire
	// a session without waiting for one.
	now func() time.Time
}

// Service satisfies the port the use cases hold.
var _ service.Uploads = (*Service)(nil)

// session is one half-received file.
type session struct {
	owner    uuid.UUID
	declared service.Declared
	incoming service.Incoming
	// deadline is when the sweeper may end this session, moved forward by
	// every chunk that arrives.
	deadline time.Time
	// busy is held for as long as a call is writing to the holder, so that two
	// chunks of one session cannot interleave inside it.
	busy bool
}

// New returns the registry over the staging that holds the bytes.
func New(staging service.Staging, expireAfter time.Duration, limit int64, logger *slog.Logger) *Service {
	return &Service{
		staging:     staging,
		expireAfter: expireAfter,
		limit:       limit,
		logger:      logger,
		sessions:    map[uuid.UUID]*session{},
		held:        map[uuid.UUID]int{},
		now:         time.Now,
	}
}

// Begin opens a session for a file the reader is about to send.
func (s *Service) Begin(
	ctx context.Context, owner uuid.UUID, declared service.Declared,
) (*service.Upload, error) {
	s.mu.Lock()

	if s.held[owner] >= perReader {
		s.mu.Unlock()

		return nil, errs.Newf(errs.KindResourceExhausted,
			"you already have %d uploads open on this node", s.held[owner]).
			WithOp(opBegin).
			WithCode(service.CodeTooManyUploads).
			WithField("upload_id", "finish or abandon one of them before beginning another")
	}

	s.mu.Unlock()

	// Opened outside the lock: it touches a disk, and a node whose every
	// upload call waits behind one file creation is a node whose uploads are
	// serialized by it.
	incoming, err := s.staging.Open(ctx, s.limit)
	if err != nil {
		return nil, err
	}

	id := uuid.New()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Checked again, because the bound was read before the lock was released
	// and another call of the same reader may have taken the last place in
	// between.
	if s.held[owner] >= perReader {
		_ = incoming.Close()

		return nil, errs.Newf(errs.KindResourceExhausted,
			"you already have %d uploads open on this node", s.held[owner]).
			WithOp(opBegin).
			WithCode(service.CodeTooManyUploads).
			WithField("upload_id", "finish or abandon one of them before beginning another")
	}

	s.sessions[id] = &session{
		owner:    owner,
		declared: declared,
		incoming: incoming,
		deadline: s.now().Add(s.expireAfter),
	}
	s.held[owner]++

	return &service.Upload{ID: id, Declared: declared}, nil
}

// Append writes a chunk at an offset and reports where the session now is.
func (s *Service) Append(
	ctx context.Context, owner, id uuid.UUID, offset int64, chunk []byte,
) (*service.Upload, error) {
	held, err := s.claim(owner, id, opAppend)
	if err != nil {
		return nil, err
	}

	defer s.release(id)

	received := held.incoming.Received()

	// The offset the caller sent is not where the node is. Nothing is written
	// and the answer carries the truth, which is what lets a caller that lost
	// a connection — or lost an answer and resent — continue from what arrived
	// rather than from the beginning.
	if offset != received {
		logging.From(ctx).DebugContext(ctx, "a chunk arrived at an offset the node was not expecting",
			slog.String("upload_id", id.String()),
			slog.Int64("offered", offset), slog.Int64("expected", received))

		return &service.Upload{ID: id, Declared: held.declared, Received: received}, nil
	}

	written, err := held.incoming.Append(chunk)
	if err != nil {
		return nil, err
	}

	return &service.Upload{ID: id, Declared: held.declared, Received: written}, nil
}

// Finish ends the session and hands over what arrived.
func (s *Service) Finish(_ context.Context, owner, id uuid.UUID) (*service.Finished, error) {
	held, err := s.claim(owner, id, opFinish)
	if err != nil {
		return nil, err
	}

	staged, err := held.incoming.Done()
	if err != nil {
		s.release(id)

		return nil, err
	}

	// Forgotten rather than released: the staged file has been handed to the
	// caller, and closing the holder now would take the bytes out from under
	// it.
	s.forget(owner, id)

	return &service.Finished{Declared: held.declared, Staged: staged}, nil
}

// Discard ends a session the caller is abandoning.
func (s *Service) Discard(_ context.Context, owner, id uuid.UUID) error {
	held, err := s.claim(owner, id, opDiscard)
	if err != nil {
		return err
	}

	_ = held.incoming.Close()

	s.forget(owner, id)

	return nil
}

// Run ends the sessions nobody is sending to, until ctx is cancelled.
//
// It is the half of the bound that a client cannot be relied on for. Discard
// covers the caller that gave up politely; this covers the one whose network
// went away, and without it a node would hold a half-received book for every
// upload that was ever interrupted.
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(sweepEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Everything still open is released on the way out, so that a node
			// shutting down does not leave descriptors to the process's death
			// to close.
			s.closeAll(ctx)

			return nil
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

// claim takes the session for the duration of one call.
//
// A session in use is refused rather than waited for. Two calls writing to one
// upload is a client bug and the answer that helps it is the offset it is
// actually at, which the caller gets by trying again.
func (s *Service) claim(owner, id uuid.UUID, op string) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	held, open := s.sessions[id]

	// A session of somebody else's is answered as though it were not here.
	// Telling a stranger that an identifier exists but is not theirs is
	// telling them when they have guessed one.
	if !open || held.owner != owner {
		return nil, errs.New(errs.KindNotFound, "this node is not holding that upload").
			WithOp(op).
			WithCode(service.CodeNoSuchUpload).
			WithField("upload_id", "it was never begun here, or it has been finished or abandoned")
	}

	if held.busy {
		return nil, errs.New(errs.KindFailedPrecondition, "another call is writing to that upload").
			WithOp(op).
			WithCode(service.CodeNoSuchUpload).
			WithField("upload_id", "send one chunk at a time, continuing from the offset the node reports")
	}

	held.busy = true

	return held, nil
}

// release hands the session back and moves its deadline forward.
//
// The deadline moves on any call that reached the session, not only on one that
// wrote bytes: what the sweeper ends is a session nobody is sending to, and a
// caller resending a chunk the node already has is a caller that is still
// there.
func (s *Service) release(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if held, open := s.sessions[id]; open {
		held.busy = false
		held.deadline = s.now().Add(s.expireAfter)
	}
}

// forget drops the session without touching its bytes.
func (s *Service) forget(owner, id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, id)

	s.held[owner]--
	if s.held[owner] <= 0 {
		delete(s.held, owner)
	}
}

// sweep ends every session past its deadline that no call is inside.
func (s *Service) sweep(ctx context.Context) {
	s.mu.Lock()

	instant := s.now()
	expired := make([]service.Incoming, 0)

	for id, held := range s.sessions {
		if held.busy || instant.Before(held.deadline) {
			continue
		}

		expired = append(expired, held.incoming)

		delete(s.sessions, id)

		s.held[held.owner]--
		if s.held[held.owner] <= 0 {
			delete(s.held, held.owner)
		}
	}

	s.mu.Unlock()

	if len(expired) == 0 {
		return
	}

	// Closed outside the lock, because each one releases a descriptor and the
	// rest of the node should not wait behind a sweep.
	for _, incoming := range expired {
		_ = incoming.Close()
	}

	s.logger.InfoContext(ctx, "uploads nobody was sending to were ended",
		slog.Int("uploads", len(expired)),
		slog.Duration("after", s.expireAfter))
}

// closeAll releases every session, for a node that is stopping.
func (s *Service) closeAll(ctx context.Context) {
	s.mu.Lock()

	open := make([]service.Incoming, 0, len(s.sessions))
	for _, held := range s.sessions {
		open = append(open, held.incoming)
	}

	s.sessions = map[uuid.UUID]*session{}
	s.held = map[uuid.UUID]int{}

	s.mu.Unlock()

	var failed error

	for _, incoming := range open {
		failed = errors.Join(failed, incoming.Close())
	}

	if len(open) > 0 {
		s.logger.InfoContext(ctx, "uploads still open were ended because the node is stopping",
			slog.Int("uploads", len(open)), logging.Err(failed))
	}
}
