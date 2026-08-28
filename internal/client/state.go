package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opState is the operation reported by this file, in the form the errs package
// expects.
const opState = "client: state"

// The permissions the state is kept under. It holds a refresh credential,
// which is the one secret a device carries that is worth stealing, so the file
// is readable by its owner alone and so is the directory it is created in.
const (
	stateFileMode      os.FileMode = 0o600
	stateDirectoryMode os.FileMode = 0o700
)

// State is what a device remembers between two runs of the client.
//
// A real Quire device keeps a database: every work, every mark, and everything
// it has not yet pushed. This client keeps the part of that a device cannot
// author correctly without — who it is, what it has already seen of each
// record it touched, and what it still owes the node — and no copy of the
// reader's collection. Holding one would mean applying incoming operations to
// it, which is a second implementation of the reconciler in this repository
// and therefore a second answer to what converges; what the collection
// currently is, is what the node's read calls report.
type State struct {
	// Server is the node this device is bound to, and the address it dials
	// when the caller names none.
	Server Server `json:"server"`
	// User is the reader this device is signed in as, as their origin server
	// named them.
	User User `json:"user"`
	// Device is this device's own identity, and the key its vector clock
	// entries are counted under. It survives a logout: a device that forgot it
	// would start a second entry that never merges with the first.
	Device Device `json:"device"`
	// Session is what the device presents, and what it refreshes with.
	Session Session `json:"session"`

	// ObservedAt is the greatest instant this device has stamped or seen, and
	// the floor its hybrid logical clock restarts from. Without it every run
	// of the client would begin having observed nothing, and a device whose
	// wall clock runs behind the node's would stamp a causally later write
	// with an earlier instant — which is the cycle C01 exists to remove.
	ObservedAt time.Time `json:"observed_at"`

	// Cursors is, per node, the last position this device has pulled. It is
	// keyed by the address dialed because the position is that node's own
	// order for the reader's log: a device that also pulls from a replica
	// keeps that node's cursor separately, and the two numbers have nothing to
	// do with each other.
	Cursors map[string]int64 `json:"cursors,omitempty"`

	// Records is the causal version this device last saw of each record it has
	// touched, keyed as [recordKey] spells it. It is what a change authored
	// offline is stamped on top of: a write ticks the clock of the version it
	// was derived from, and a device that had forgotten that version would
	// author a first write to a record that already has a history.
	Records map[string]Record `json:"records,omitempty"`

	// Pending is what this device authored while disconnected, in the order it
	// authored it. It is drained by [Client.Push].
	Pending []Operation `json:"pending,omitempty"`
}

// Server is the node a device is bound to.
type Server struct {
	// Address is the authority to dial for gRPC, as host:port.
	Address string `json:"address"`
	// Domain is the federation domain of the node, which is the second half of
	// every identifier it hosts.
	Domain string `json:"domain,omitempty"`
}

// User is the reader a device is signed in as.
type User struct {
	ID          uuid.UUID `json:"id"`
	LocalName   string    `json:"local_name,omitempty"`
	FederatedID string    `json:"federated_id,omitempty"`
}

// Device is this device's own identity.
type Device struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name,omitempty"`
	Platform string    `json:"platform,omitempty"`
}

// Session is what a device presents and what it refreshes with.
type Session struct {
	AccessToken           string    `json:"access_token,omitempty"`
	AccessTokenExpiresAt  time.Time `json:"access_token_expires_at,omitempty"`
	RefreshToken          string    `json:"refresh_token,omitempty"`
	RefreshTokenExpiresAt time.Time `json:"refresh_token_expires_at,omitempty"`
}

// IsZero reports whether the device holds no session at all.
func (s *Session) IsZero() bool { return s.AccessToken == "" && s.RefreshToken == "" }

// Record is the causal version this device last saw of one record.
//
// The identifier is kept beside the clock because two of the five records are
// addressed by their natural key rather than by the identifier the operation
// names (C18): the filing of a work under a grouping and a reading position
// carry a surrogate key each replica mints for itself, so the one this device
// minted has to be remembered or it would mint another on every write.
type Record struct {
	ID    uuid.UUID        `json:"id"`
	Clock crdt.VectorClock `json:"vector_clock,omitempty"`
}

// Operation is one change this device authored and has not yet handed to the
// node.
//
// The field names are the contract's, so that the log on disk reads as what it
// is: the messages this device owes, in the order it wrote them.
type Operation struct {
	ID          uuid.UUID                  `json:"id"`
	DeviceID    uuid.UUID                  `json:"device_id"`
	Entity      string                     `json:"target_entity"`
	TargetID    uuid.UUID                  `json:"target_id"`
	Kind        string                     `json:"operation"`
	Delta       map[string]json.RawMessage `json:"delta"`
	VectorClock crdt.VectorClock           `json:"vector_clock,omitempty"`
	CreatedAt   time.Time                  `json:"created_at"`
}

// recordKey addresses a record in [State.Records].
//
// It is the entity and the identity of the record, which for three of the five
// is the record's own identifier and for the other two is the natural key the
// reconciler resolves them by. The device is never part of it: this state
// belongs to one device, and the only reading position it can author is its
// own.
func recordKey(entity string, parts ...uuid.UUID) string {
	key := entity

	for _, part := range parts {
		key += ":" + part.String()
	}

	return key
}

// loadState reads the state from path.
//
// A path that does not exist is not an error: it is a device that has never
// run, and what it gets is an empty state that the first login fills in.
func loadState(path string) (*State, error) {
	state := &State{
		Cursors: map[string]int64{},
		Records: map[string]Record{},
	}

	// The path is the operator's own state file, named by them.
	content, err := os.ReadFile(path) //nolint:gosec // the caller names their own state file
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}

		return nil, errs.Wrap(err, errs.KindInternal, "the device state could not be read").
			WithOp(opState)
	}

	if err = json.Unmarshal(content, state); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "the device state could not be read").
			WithOp(opState).
			WithField("state", "the file is not the state this client writes")
	}

	if state.Cursors == nil {
		state.Cursors = map[string]int64{}
	}

	if state.Records == nil {
		state.Records = map[string]Record{}
	}

	return state, nil
}

// save writes the state to path.
//
// It writes a neighbouring file and renames it over the target, so that a
// client interrupted halfway through leaves the previous state rather than
// half of the new one. A device whose state is truncated has lost its clock,
// and a lost clock is a device that authors writes nothing can order.
func (s *State) save(path string) error {
	failure := func(err error) error {
		return errs.Wrap(err, errs.KindInternal, "the device state could not be written").
			WithOp(opState)
	}

	if err := os.MkdirAll(filepath.Dir(path), stateDirectoryMode); err != nil {
		return failure(err)
	}

	content, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return failure(err)
	}

	temporary := path + ".tmp"
	if err = os.WriteFile(temporary, append(content, '\n'), stateFileMode); err != nil {
		return failure(err)
	}

	if err = os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)

		return failure(err)
	}

	return nil
}
