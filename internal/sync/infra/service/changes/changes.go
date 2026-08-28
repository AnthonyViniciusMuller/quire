// Package changes is the in-process hub that tells an open sync stream that
// the reader's log has grown.
//
// It is what makes the stream of the contract a stream rather than a poll: a
// change pushed by one device reaches the reader's other devices as it happens,
// because the call that stored it wakes the streams that are waiting.
//
// # What it is not
//
// It is a hint, and everything downstream is written to be correct without it.
// A stream that misses one finds the change on its next poll; a device that
// misses both finds it at its next pull, because the cursor is what the design
// rests on. That is why Announce cannot fail: there is nothing a caller could
// do about a hint that was not delivered, and a change that is already
// committed is not lost by being announced late.
//
// It is also in-process, which is the limitation worth stating plainly. Two
// devices of one reader whose streams are open against two replicas of this
// node do not wake each other, and they fall back to the poll. Fixing that
// needs the announcement to leave the process — a LISTEN/NOTIFY on the
// database, or a bus — and the shape here does not change when it does: the
// hub grows a second implementation behind the same two methods.
package changes

import (
	"sync"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/sync/application/service"
)

// Hub carries announcements to the streams waiting for them.
//
// The zero value is not usable; build one with [New].
type Hub struct {
	mu sync.Mutex
	// watchers is one set of listeners per reader, keyed so that a hub with a
	// thousand open streams wakes only the ones the change concerns.
	watchers map[uuid.UUID]map[*watcher]struct{}
}

// Hub satisfies the port the use cases hold.
var _ service.Changes = (*Hub)(nil)

// watcher is one open stream's end of the hub.
type watcher struct {
	// woken is buffered by one, which is what makes the hint coalescing rather
	// than queued: a stream that has not looked yet is already going to look,
	// so a second announcement before it does adds nothing.
	woken chan struct{}
}

// New returns an empty hub.
func New() *Hub {
	return &Hub{watchers: map[uuid.UUID]map[*watcher]struct{}{}}
}

// Announce reports that the reader's log has grown.
//
// It never blocks: a listener that has not yet looked at the hint it already
// has is skipped, because it is going to look.
func (h *Hub) Announce(userID uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for listener := range h.watchers[userID] {
		select {
		case listener.woken <- struct{}{}:
		default:
		}
	}
}

// Watch returns a channel that receives when the reader's log grows, and the
// function that stops watching.
//
// The caller must call the second, and a stream that returns without doing so
// leaks a listener the hub will wake for as long as the node runs. It is
// returned rather than tied to a context so that the release is visible at the
// call site as a defer.
func (h *Hub) Watch(userID uuid.UUID) (woken <-chan struct{}, release func()) {
	listener := &watcher{woken: make(chan struct{}, 1)}

	h.mu.Lock()

	if _, watching := h.watchers[userID]; !watching {
		h.watchers[userID] = map[*watcher]struct{}{}
	}

	h.watchers[userID][listener] = struct{}{}

	h.mu.Unlock()

	return listener.woken, func() { h.forget(userID, listener) }
}

// forget removes a listener, and the reader's set with it when it was the last
// one.
func (h *Hub) forget(userID uuid.UUID, listener *watcher) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.watchers[userID], listener)

	if len(h.watchers[userID]) == 0 {
		delete(h.watchers, userID)
	}
}

// Watching is how many streams are listening for a reader, which is what a
// test asserts the release against.
func (h *Hub) Watching(userID uuid.UUID) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return len(h.watchers[userID])
}
