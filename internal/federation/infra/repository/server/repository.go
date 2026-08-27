// Package server is the PostgreSQL adapter of the catalogue repository: it
// satisfies the port declared in internal/federation/domain/server and is the
// only place that knows federation.servers exists.
package server

import (
	"context"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/persist/federationdb"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opEnsureLocal = "federation/server: ensure local"
	opCreate      = "federation/server: create"
	opUpdate      = "federation/server: update"
	opDelete      = "federation/server: delete"
	opGetByID     = "federation/server: get by id"
	opGetByDomain = "federation/server: get by domain"
	opList        = "federation/server: list"
)

// constraintDomain is the name of the unique constraint on the domain, as it
// appears in the driver error. It is what tells a node the catalogue already
// holds from any other write failure.
const constraintDomain = "servers_domain_key"

// Repository reads and writes the catalogue in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ server.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in.
func (r *Repository) queries(ctx context.Context) *federationdb.Queries {
	return federationdb.New(r.manager.Executor(ctx))
}

// EnsureLocal creates or refreshes the row that says which node this is.
//
// The statement runs on whatever the context is running in, so a registration
// that opened a transaction gets the row inside it. That is deliberate: the
// row the reader is about to reference and the reader themselves then commit
// together, and a rolled back registration leaves no half-written catalogue.
func (r *Repository) EnsureLocal(ctx context.Context, descriptor server.Descriptor) (*server.Server, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}

	row, err := r.queries(ctx).EnsureLocalServer(ctx, federationdb.EnsureLocalServerParams{
		Domain:  descriptor.Domain.String(),
		BaseUrl: descriptor.BaseURL.String(),
		JwksUri: optionalString(descriptor.JWKSURI.String()),
	})
	if err != nil {
		return nil, persist.Classify(err, opEnsureLocal)
	}

	return toDomain(&row), nil
}

// Create records a peer, naming the catalogue's own uniqueness rule when it
// was the one broken.
func (r *Repository) Create(ctx context.Context, node *server.Server) error {
	err := r.queries(ctx).CreateServer(ctx, federationdb.CreateServerParams{
		ID:                     node.ID,
		Domain:                 node.Domain.String(),
		BaseUrl:                node.BaseURL.String(),
		JwksUri:                optionalString(node.JWKSURI.String()),
		CertificateFingerprint: optionalString(node.CertificateFingerprint.String()),
		DiscoveredAt:           optionalTime(node.DiscoveredAt),
		Active:                 node.Active,
	})

	if persist.IsUniqueViolation(err, constraintDomain) {
		return errs.Wrap(err, errs.KindAlreadyExists, "that node is already in the catalogue").
			WithOp(opCreate).
			WithCode(server.CodeDomainKnown).
			WithField("domain", "it is already known here, possibly because another reader added it")
	}

	return persist.Classify(err, opCreate)
}

// Update writes back what a refresh learned and whether the node takes part.
func (r *Repository) Update(ctx context.Context, node *server.Server) error {
	rows, err := r.queries(ctx).UpdateServer(ctx, federationdb.UpdateServerParams{
		ID:                     node.ID,
		BaseUrl:                node.BaseURL.String(),
		JwksUri:                optionalString(node.JWKSURI.String()),
		CertificateFingerprint: optionalString(node.CertificateFingerprint.String()),
		DiscoveredAt:           optionalTime(node.DiscoveredAt),
		Active:                 node.Active,
	})
	if err != nil {
		return persist.Classify(err, opUpdate)
	}

	// An UPDATE that matched nothing is not an error to PostgreSQL, and it is
	// exactly what a node forgotten between the read and the write looks like.
	if rows == 0 {
		return notFound(nil, opUpdate)
	}

	return nil
}

// Delete forgets the peer.
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	rows, err := r.queries(ctx).DeleteServer(ctx, id)
	if err != nil {
		return persist.Classify(err, opDelete)
	}

	if rows == 0 {
		return notFound(nil, opDelete)
	}

	return nil
}

// GetByID reads a node by primary key, active or not.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*server.Server, error) {
	row, err := r.queries(ctx).GetServerByID(ctx, id)
	if err != nil {
		return nil, readError(err, opGetByID)
	}

	return toDomain(&row), nil
}

// GetByDomain reads a node by the authority it is known as.
func (r *Repository) GetByDomain(ctx context.Context, domain server.Domain) (*server.Server, error) {
	row, err := r.queries(ctx).GetServerByDomain(ctx, domain.String())
	if err != nil {
		return nil, readError(err, opGetByDomain)
	}

	return toDomain(&row), nil
}

// List reads the catalogue.
func (r *Repository) List(ctx context.Context, includeInactive bool) ([]*server.Server, error) {
	rows, err := r.queries(ctx).ListServers(ctx, includeInactive)
	if err != nil {
		return nil, persist.Classify(err, opList)
	}

	nodes := make([]*server.Server, 0, len(rows))
	for index := range rows {
		nodes = append(nodes, toDomain(&rows[index]))
	}

	return nodes, nil
}

// readError is the classification both single-row reads share.
func readError(err error, op string) error {
	if persist.IsNoRows(err) {
		return notFound(err, op)
	}

	return persist.Classify(err, op)
}

// notFound is the answer to a node that is not in the catalogue.
func notFound(cause error, op string) error {
	return errs.Wrap(cause, errs.KindNotFound, "no such node in the catalogue").
		WithOp(op).
		WithCode(server.CodeNotFound)
}

// toDomain rebuilds the entity from the row, restoring rather than
// constructing: the identifier is the one every reader hosted here references.
func toDomain(row *federationdb.FederationServer) *server.Server {
	props := server.Props{
		Descriptor: server.Descriptor{
			Domain:  server.Domain(row.Domain),
			BaseURL: server.BaseURL(row.BaseUrl),
		},
		IsLocal: row.IsLocal,
		Active:  row.Active,
	}

	// The three nullable columns. Absent means the node published none, which
	// the domain reads as the zero value of each.
	if row.JwksUri != nil {
		props.JWKSURI = server.JWKSURI(*row.JwksUri)
	}

	if row.CertificateFingerprint != nil {
		props.CertificateFingerprint = server.Fingerprint(*row.CertificateFingerprint)
	}

	if row.DiscoveredAt != nil {
		props.DiscoveredAt = *row.DiscoveredAt
	}

	return server.Restore(row.ID, &props)
}

// optionalString renders an absent value as the NULL the column holds.
func optionalString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

// optionalTime renders the zero instant as the NULL a row nothing discovered
// holds.
func optionalTime(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}

	return &at
}
