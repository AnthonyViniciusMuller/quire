package client

import (
	"context"
	"uuid"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// pathActive is the one field of a known node an update may name. Everything
// else in the record was learned from the node itself and is refreshed rather
// than typed.
const pathActive = "active"

// Migration is a reader moving to this node from another one (UC16, RF17).
//
// It is addressed to the node being moved *to*, by a device that already holds
// the reader's collection, which is what lets it proceed without the previous
// node's cooperation or availability.
type Migration struct {
	LocalName   string
	DisplayName string
	Email       string
	Password    string

	// PreviousFederatedID is the identifier the reader is arriving from,
	// recorded as provenance. This node cannot verify it (C11) and must not
	// treat it as authenticated.
	PreviousFederatedID string

	// Devices are the reader's other devices, to be adopted with the
	// identifiers they already hold. This device is added to them by the
	// client: it is the one making the call, and the session comes back for
	// it.
	//
	// Omitting a device is not a small mistake. Every operation names its
	// authoring device and every vector clock is keyed by a device id, so a
	// device that was left out arrives later as a log this node cannot insert.
	Devices []Device
}

// Discover resolves a domain to the services it exposes, over /.well-known as
// RFC 8615 establishes, and stores nothing (UC13).
func (c *Client) Discover(ctx context.Context, domain string) (*quirev1.ServerDescriptor, error) {
	authorized, err := c.call(ctx, "discover a node")
	if err != nil {
		return nil, err
	}

	response, err := c.federation.DiscoverServer(authorized, &quirev1.DiscoverServerRequest{Domain: domain})
	if err != nil {
		return nil, err
	}

	return response.GetDescriptor_(), nil
}

// AddKnownServer discovers a domain and records what it found, pinning the
// certificate fingerprint that node-to-node mTLS is checked against (UC12,
// RNF08).
//
// Only the domain is sent. The rest of the record is what the node says about
// itself, because a reader who could type the base URL or the fingerprint by
// hand could also pin the wrong one.
func (c *Client) AddKnownServer(ctx context.Context, domain string) (*quirev1.Server, error) {
	authorized, err := c.call(ctx, "add a node")
	if err != nil {
		return nil, err
	}

	response, err := c.federation.AddKnownServer(authorized, &quirev1.AddKnownServerRequest{Domain: domain})
	if err != nil {
		return nil, err
	}

	return response.GetServer(), nil
}

// GetKnownServer returns one node from the reader's catalogue.
func (c *Client) GetKnownServer(ctx context.Context, server uuid.UUID) (*quirev1.Server, error) {
	authorized, err := c.call(ctx, "get a node")
	if err != nil {
		return nil, err
	}

	response, err := c.federation.GetKnownServer(authorized,
		&quirev1.GetKnownServerRequest{ServerId: server.String()})
	if err != nil {
		return nil, err
	}

	return response.GetServer(), nil
}

// ListKnownServers returns the catalogue. Deactivated nodes are hidden unless
// they are asked for.
func (c *Client) ListKnownServers(ctx context.Context, includeInactive bool) ([]*quirev1.Server, error) {
	authorized, err := c.call(ctx, "list the nodes")
	if err != nil {
		return nil, err
	}

	response, err := c.federation.ListKnownServers(authorized,
		&quirev1.ListKnownServersRequest{IncludeInactive: includeInactive})
	if err != nil {
		return nil, err
	}

	return response.GetServers(), nil
}

// SetKnownServerActive decides whether a node takes part in replication.
// Clearing it stops the traffic without losing what discovery already learned.
func (c *Client) SetKnownServerActive(
	ctx context.Context, server uuid.UUID, active bool,
) (*quirev1.Server, error) {
	authorized, err := c.call(ctx, "update a node")
	if err != nil {
		return nil, err
	}

	response, err := c.federation.UpdateKnownServer(authorized, &quirev1.UpdateKnownServerRequest{
		ServerId:   server.String(),
		Server:     &quirev1.Server{Active: active},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{pathActive}},
	})
	if err != nil {
		return nil, err
	}

	return response.GetServer(), nil
}

// RefreshKnownServer re-runs discovery against a node already in the
// catalogue.
//
// It is the only way a pinned fingerprint changes, and it is a deliberate act
// by the reader precisely because a node that re-pinned on its own would have
// no pin at all. The reply says whether the fingerprint changed, and it is
// reported rather than applied silently: a rotation and an interception look
// identical from here.
func (c *Client) RefreshKnownServer(
	ctx context.Context, server uuid.UUID,
) (*quirev1.Server, bool, error) {
	authorized, err := c.call(ctx, "refresh a node")
	if err != nil {
		return nil, false, err
	}

	response, err := c.federation.RefreshKnownServer(authorized,
		&quirev1.RefreshKnownServerRequest{ServerId: server.String()})
	if err != nil {
		return nil, false, err
	}

	return response.GetServer(), response.GetCertificateFingerprintChanged(), nil
}

// RemoveKnownServer forgets a node. It is refused while an active
// authorization names it: forgetting a peer that still holds a copy would leave
// the reader unable to revoke it, and RN03 is the promise that they can.
func (c *Client) RemoveKnownServer(ctx context.Context, server uuid.UUID) error {
	authorized, err := c.call(ctx, "remove a node")
	if err != nil {
		return err
	}

	_, err = c.federation.RemoveKnownServer(authorized,
		&quirev1.RemoveKnownServerRequest{ServerId: server.String()})

	return err
}

// AuthorizeReplica allows a known node to hold a copy of this reader's data,
// with or without their files (UC15, RF16).
func (c *Client) AuthorizeReplica(
	ctx context.Context, server uuid.UUID, replicatesFiles bool,
) (*quirev1.ReplicaAuthorization, error) {
	authorized, err := c.call(ctx, "authorize a replica")
	if err != nil {
		return nil, err
	}

	response, err := c.federation.AuthorizeReplica(authorized, &quirev1.AuthorizeReplicaRequest{
		ServerId:        server.String(),
		ReplicatesFiles: replicatesFiles,
	})
	if err != nil {
		return nil, err
	}

	return response.GetAuthorization(), nil
}

// RevokeReplica withdraws that permission.
//
// The authorization is deactivated and kept, because the record that it once
// existed is what explains why a peer still holds data: revoking stops the
// replication, it does not reach into another operator's database.
func (c *Client) RevokeReplica(ctx context.Context, server uuid.UUID) error {
	authorized, err := c.call(ctx, "revoke a replica")
	if err != nil {
		return err
	}

	_, err = c.federation.RevokeReplica(authorized, &quirev1.RevokeReplicaRequest{ServerId: server.String()})

	return err
}

// ListReplicaAuthorizations returns which nodes hold a copy, which of them hold
// the files, and which used to.
func (c *Client) ListReplicaAuthorizations(
	ctx context.Context, includeInactive bool,
) ([]*quirev1.ReplicaAuthorization, error) {
	authorized, err := c.call(ctx, "list the replicas")
	if err != nil {
		return nil, err
	}

	response, err := c.federation.ListReplicaAuthorizations(authorized,
		&quirev1.ListReplicaAuthorizationsRequest{IncludeInactive: includeInactive})
	if err != nil {
		return nil, err
	}

	return response.GetAuthorizations(), nil
}

// Migrate moves the reader to the node this client is talking to (UC16).
//
// It needs no session, because the reader has no account here yet — which is
// the same reason registering needs none.
//
// What it does to the device state is the point of the call, and it is the
// opposite of what a login to another account does. The reader's identifier
// changes, since the domain half is this node, but the device keeps its
// identifier, its clock and its pending log: those are what the devices then
// push through SyncService, and a client that cleared them on the change of
// account would have completed the migration by discarding what was being
// migrated (C11).
//
// The cursor for this node is dropped rather than kept. It is a position in
// this node's log, this node's log for this reader starts empty, and a number
// left over from some earlier conversation with the same address would skip
// everything the migration is about to write.
func (c *Client) Migrate(ctx context.Context, in *Migration) (*quirev1.MigrateHomeServerResponse, error) {
	if err := c.requireOnline("migrate"); err != nil {
		return nil, err
	}

	response, err := c.federation.MigrateHomeServer(ctx, &quirev1.MigrateHomeServerRequest{
		LocalName:           in.LocalName,
		DisplayName:         in.DisplayName,
		Email:               in.Email,
		Password:            in.Password,
		PreviousFederatedId: in.PreviousFederatedID,
		Devices:             c.adopting(in.Devices),
	})
	if err != nil {
		return nil, err
	}

	reader := response.GetUser()

	c.state.User = User{
		ID:          parseID(reader.GetId()),
		LocalName:   reader.GetLocalName(),
		FederatedID: reader.GetFederatedId(),
	}
	c.state.Server = Server{Address: c.Address(), Domain: reader.GetOriginServerDomain()}

	delete(c.state.Cursors, c.Address())

	c.storeSession(response.GetSession())

	if err := c.save(); err != nil {
		return nil, err
	}

	return response, nil
}

// adopting is the list of devices the migration carries: this one, and the
// others the caller named.
//
// This device is first and is never left out, because the session comes back
// for it and because a device that migrated an account without itself would
// have nowhere to push from. A caller that names it again does not duplicate
// it — sending a device twice is not an error, but a client that can avoid it
// should.
func (c *Client) adopting(others []Device) []*quirev1.Device {
	devices := make([]*quirev1.Device, 0, len(others)+1)
	named := make(map[uuid.UUID]struct{}, len(others)+1)

	if own := c.state.Device; own.ID != (uuid.UUID{}) {
		named[own.ID] = struct{}{}
		devices = append(devices, &quirev1.Device{
			Id:       own.ID.String(),
			Name:     own.Name,
			Platform: own.Platform,
			Active:   true,
		})
	}

	for _, other := range others {
		if _, already := named[other.ID]; already {
			continue
		}

		named[other.ID] = struct{}{}
		devices = append(devices, &quirev1.Device{
			Id:       other.ID.String(),
			Name:     other.Name,
			Platform: other.Platform,
			Active:   true,
		})
	}

	return devices
}
