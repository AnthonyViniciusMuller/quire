// Package client is the device half of the Quire protocol, as a library.
//
// It is the reference client of D05 in docs/tcc-corrections.md: what the
// end-to-end suites drive, and what stands in for the Flutter application when
// the system is demonstrated. cmd/quirectl is the terminal program over it and
// decides nothing — it decodes flags, calls one method here, and prints what
// comes back.
//
// # It is a device, not a caller
//
// The distinction is the whole reason this package exists rather than a handful
// of generated stubs. A device is bound to the reader's origin server, is named
// by an identifier every vector clock entry is keyed by, and carries a clock of
// its own; it authors changes whether or not it can reach the node, and hands
// them over when it can. All of that lives between two runs of a command, so it
// lives in a file — [State] — and every method here reads and writes it.
//
// # The two paths are one method
//
// LibraryService and ReadingService are the connected path to a change and
// SyncService.PushOperations is the disconnected one, and the contract is
// emphatic that a change made either way has to be indistinguishable once
// applied. So a write is one method here, and which path it takes is a property
// of the client and not of the call: a client opened with [Options.Offline]
// stamps the change on this device's clock and appends it to the local log,
// and one opened without it calls the RPC and lets the node stamp it. The
// caller gets a [Written] either way and does not branch.
//
// # What it does not do
//
// It reads no environment variable and resolves no default path, for the reason
// nothing below cmd/quired does: the configuration surface of a program should
// be enumerable by reading one struct, and that struct is [Options], filled in
// by cmd/quirectl.
//
// It also keeps no copy of the reader's collection. What it remembers of a
// record is the causal version it last saw, which is what a later change has to
// be stamped on top of; what the collection currently *is*, is what the node's
// read calls report. A local replica would have to be maintained by applying
// incoming operations to it, which is a second reconciler in this repository
// and therefore a second answer to what RN02 converges on.
package client

import (
	"context"
	"crypto/tls"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/hlc"
)

// opClient is the operation reported by this file, in the form the errs package
// expects.
const opClient = "client: call"

// The stable machine-readable codes this package raises.
const (
	// CodeNoServer is a call made by a client that was given no address and
	// has never been bound to a node.
	CodeNoServer = "no_server"
	// CodeNoSession is a call that needs a session by a device that holds
	// none, or holds one that has expired beyond refreshing.
	CodeNoSession = "no_session"
	// CodeNoDevice is a change authored by a client that has never been bound
	// to a device, which is a change no clock could attribute to anybody.
	CodeNoDevice = "no_device"
	// CodeOffline is a call that cannot be made without the node.
	CodeOffline = "offline"
)

// refreshMargin is how long before its expiry an access token is exchanged.
//
// A token that expires while the call it authorizes is in flight is refused by
// the node, and the caller cannot tell that from a token that was never valid.
// The margin is what keeps the two apart, and it is a constant because it is a
// property of the round trip and not something an operator should tune.
const refreshMargin = 30 * time.Second

// authorizationHeader is the metadata key a bearer token travels under, and
// bearerScheme is what precedes it. Both are what the node's interceptor reads.
const (
	authorizationHeader = "authorization"
	bearerScheme        = "Bearer "
)

// Options is everything the client is configured by. There is nothing else: no
// variable is read and no default path is resolved here.
type Options struct {
	// Address is the authority to dial for gRPC, as host:port. Empty means the
	// node this device is already bound to, which is what the state remembers
	// after the first login.
	Address string

	// StatePath is the file the device state is kept in. It is required, and
	// it is what makes two devices on one machine two devices.
	StatePath string

	// Offline makes every write take the disconnected path: the change is
	// stamped on this device's clock and appended to the local log, to be
	// handed over by the next push. Every call that only the node can answer
	// is refused while it is set.
	Offline bool

	// Plaintext dials without TLS, which is what the local federation of
	// docker compose answers on: RFC 8615 puts the discovery documents on
	// plain HTTP, so a deployment without a gateway in front of it serves gRPC
	// on a port of its own with no certificate.
	Plaintext bool

	// CACertificate is a PEM file to verify the node against, for a deployment
	// whose certificate is not signed by a public authority. Empty verifies
	// against the system roots.
	CACertificate string
}

// Client is one device talking to one node.
//
// It is not safe for concurrent use. A device is a device: two goroutines
// authoring changes through one client would tick one clock twice for one
// event, which is the one thing a vector clock cannot survive.
type Client struct {
	options Options

	connection *grpc.ClientConn
	auth       quirev1.AuthServiceClient
	library    quirev1.LibraryServiceClient
	reading    quirev1.ReadingServiceClient
	federation quirev1.FederationServiceClient
	sync       quirev1.SyncServiceClient

	state *State

	// clock is this device's hybrid logical clock, seeded from what the state
	// remembers having observed. It stamps every change authored offline, and
	// it observes every instant that arrives from the node — which is how a
	// device that has been away keeps stamping after what it missed rather
	// than underneath it.
	clock *hlc.Clock
}

// Open loads the device state and, unless the client is offline, dials the
// node.
//
// The connection is lazy, as gRPC connections are: an address that answers
// nothing fails at the first call rather than here, which is what lets a client
// be opened in order to be told it has no session.
func Open(options Options) (*Client, error) {
	if options.StatePath == "" {
		return nil, errs.New(errs.KindInvalidArgument, "the client was given no state file").
			WithOp(opClient).
			WithField("state_path", "a device is the state it keeps, so the path is required")
	}

	state, err := loadState(options.StatePath)
	if err != nil {
		return nil, err
	}

	client := &Client{options: options, state: state, clock: hlc.New()}

	// The floor the device restarts from. A reading further ahead of this
	// machine's wall clock than the clock tolerates is refused by it, which is
	// the same answer it gives a peer whose clock is broken.
	client.clock.Observe(state.ObservedAt)

	if options.Offline {
		return client, nil
	}

	if err := client.dial(); err != nil {
		return nil, err
	}

	return client, nil
}

// dial opens the connection and the five service clients over it.
func (c *Client) dial() error {
	address := c.Address()
	if address == "" {
		return errs.New(errs.KindFailedPrecondition, "the client does not know which node to call").
			WithOp(opClient).
			WithCode(CodeNoServer).
			WithField("address", "name a node, or log in to one once")
	}

	transport, err := c.transportCredentials()
	if err != nil {
		return err
	}

	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(transport))
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "the node could not be reached").
			WithOp(opClient).
			WithField("address", "it is not an authority this client can dial")
	}

	c.connection = connection
	c.auth = quirev1.NewAuthServiceClient(connection)
	c.library = quirev1.NewLibraryServiceClient(connection)
	c.reading = quirev1.NewReadingServiceClient(connection)
	c.federation = quirev1.NewFederationServiceClient(connection)
	c.sync = quirev1.NewSyncServiceClient(connection)

	return nil
}

// transportCredentials is what the connection is protected by.
//
// TLS is the default and plaintext is asked for, which is the way round a
// client should be: the deployment that needs plaintext is the developer's own
// federation, and it is the one whose operator knows they are asking for it.
func (c *Client) transportCredentials() (credentials.TransportCredentials, error) {
	switch {
	case c.options.Plaintext:
		return insecure.NewCredentials(), nil
	case c.options.CACertificate != "":
		transport, err := credentials.NewClientTLSFromFile(c.options.CACertificate, "")
		if err != nil {
			return nil, errs.Wrap(err, errs.KindInvalidArgument, "the authority certificate could not be read").
				WithOp(opClient).
				WithField("ca_certificate", "it is not a PEM file of certificates")
		}

		return transport, nil
	default:
		return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12}), nil
	}
}

// Close releases the connection. The state is not written here: every method
// that changes it writes it, so a client that is dropped without being closed
// has lost nothing.
func (c *Client) Close() error {
	if c.connection == nil {
		return nil
	}

	return c.connection.Close()
}

// Address is the node this client calls: the one it was given, or the one the
// device is bound to.
func (c *Client) Address() string {
	if c.options.Address != "" {
		return c.options.Address
	}

	return c.state.Server.Address
}

// State is what the device remembers. It is returned for the caller to read and
// to print; changing it behind the client's back changes what the next push
// claims.
func (c *Client) State() *State { return c.state }

// IsOffline reports which path a write will take.
func (c *Client) IsOffline() bool { return c.options.Offline }

// save writes the state, and is called by every method that changed it.
func (c *Client) save() error {
	c.state.ObservedAt = c.clock.Observed()

	return c.state.save(c.options.StatePath)
}

// requireOnline refuses a call that only the node can answer.
//
// The message names the call, because the refusal is a statement about this
// client and not about the contract: every one of these is a call the node
// serves perfectly well to a device that can reach it.
func (c *Client) requireOnline(call string) error {
	if !c.options.Offline {
		return nil
	}

	return errs.New(errs.KindFailedPrecondition, "the client is offline").
		WithOp(opClient).
		WithCode(CodeOffline).
		WithField(call, "only the node can answer it")
}

// requireDevice returns the device authoring a change.
//
// A client that has never been bound cannot author one at all: the identifier
// is what every vector clock entry is keyed by, so a change from an unbound
// device is a change no node could attribute to anybody (RN10).
func (c *Client) requireDevice() (uuid.UUID, error) {
	if c.state.Device.ID == (uuid.UUID{}) {
		return uuid.UUID{}, errs.New(errs.KindFailedPrecondition, "this client is not bound to a device").
			WithOp(opClient).
			WithCode(CodeNoDevice).
			WithField("device", "log in once while connected, which is what binds it")
	}

	return c.state.Device.ID, nil
}

// authorized returns a context presenting this device's access token,
// refreshing the session first if the token is about to expire.
//
// Refreshing here rather than in each method is what keeps a device that has
// been idle since yesterday from failing its next call: the call that needs the
// credential is the call that renews it. A stream is authenticated when it is
// opened and not again, so what this bounds there is how long a device may have
// been away before it can open one.
func (c *Client) authorized(ctx context.Context) (context.Context, error) {
	if c.state.Session.IsZero() {
		return nil, errs.New(errs.KindUnauthenticated, "this device has no session").
			WithOp(opClient).
			WithCode(CodeNoSession).
			WithField("session", "log in first")
	}

	if time.Until(c.state.Session.AccessTokenExpiresAt) < refreshMargin {
		if err := c.Refresh(ctx); err != nil {
			return nil, err
		}
	}

	return metadata.AppendToOutgoingContext(ctx,
		authorizationHeader, bearerScheme+c.state.Session.AccessToken), nil
}

// observe folds an instant that arrived from elsewhere into this device's
// clock, so that what it stamps next is stamped after what it has just seen.
func (c *Client) observe(at *quirev1.HybridTimestamp) {
	if at == nil {
		return
	}

	c.clock.Observe(time.UnixMicro(at.GetUnixMicros()).UTC())
}

// remember records the causal version this device has just seen of a record,
// under the key it is addressed by.
//
// The clock is merged rather than replaced. A device that has authored a change
// it has not yet pushed knows a version the node does not, and a reply that
// overwrote the local clock with the node's would let the next write be stamped
// underneath a write this device itself made.
func (c *Client) remember(key string, id uuid.UUID, clock crdt.VectorClock) {
	known := c.state.Records[key]
	c.state.Records[key] = Record{ID: id, Clock: known.Clock.Merge(clock)}
}

// rememberRevision is [Client.remember] for a record that carries the whole
// revision, and observes the instant it was stamped at.
func (c *Client) rememberRevision(key string, id uuid.UUID, revision *quirev1.Revision) {
	if revision == nil {
		return
	}

	c.observe(revision.GetUpdatedAt())
	c.remember(key, id, readClock(revision.GetVectorClock()))
}

// readClock reads a causal version off the wire.
//
// Zero entries are dropped, which the contract requires of a receiver: a device
// absent from the map and a device mapped to zero are the same causal history,
// and a client that kept both forms would make one history compare unequal to
// itself.
func readClock(clock *quirev1.VectorClock) crdt.VectorClock {
	entries := clock.GetEntries()

	read := make(crdt.VectorClock, len(entries))

	for device, counter := range entries {
		if counter > 0 {
			read[crdt.DeviceID(device)] = counter
		}
	}

	return read
}

// parseID reads an identifier the node sent, which a client has no business
// refusing: a malformed one is the zero value, and every use of it is a lookup
// that will not find anything.
func parseID(value string) uuid.UUID {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}
	}

	return id
}
