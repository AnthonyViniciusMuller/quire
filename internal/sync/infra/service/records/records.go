// Package records is the reconciler: the adapter that merges an operation into
// the record it names, in the five tables the node replicates.
//
// It is the only place that knows what those tables are. The sync slice owns
// the log and owns nothing that travels through it, so the vocabulary of a
// work, a grouping, a filing, a mark and a position lives here, behind the
// one-method Records port the use cases hold — and every one of them is reached
// through the repository its own slice declares, never through a query of this
// slice's own. That is the shape internal/reading/infra/service/works set, at
// the size the sync slice needs it.
//
// # What it decides, and what it does not
//
// It decides nothing about which version of a record survives. That is
// crdt.Revision.Supersedes, in the shared core, where the merge laws are proved
// and where C01's tie-break lives. What this package contributes is the three
// things the rule cannot do for itself: resolve the record an operation names,
// decode the fields its delta claims, and write the result.
//
// # The delta's vocabulary
//
// A delta names the record's fields as the schema and the contract both name
// them — "title", "locator", "ebook_id" — and carries their values in the form
// the column holds: a kind is "note" and not the contract's enum constant, an
// instant is RFC 3339, extra metadata is an object. That is the same principle
// TargetEntity follows, and for the same reason: one vocabulary travels through
// the contract, this node's schema and the SQLite schema on the device, and a
// second one would have to be translated at every hop. The specification says
// nothing about the shape of carga_delta at all, which is C19 in
// docs/tcc-corrections.md.
//
// # Two records are addressed by their natural key
//
// A filing and a reading position carry a surrogate identifier that each
// replica mints for itself, so two devices that file the same work under the
// same shelf while offline produce two different identifiers for one record.
// Those two are therefore resolved by the key the schema makes unique — the
// pair — taken from the delta, and the operation's target identifier is the
// author's own row and a label. C18 in docs/tcc-corrections.md is the finding.
package records

import (
	"context"
	"errors"

	librarycollection "github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	libraryebook "github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	librarymembership "github.com/anthonyvsmuller/quire/internal/library/domain/membership"
	readingannotation "github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	readingprogress "github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/application/service"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// opReconcile is the operation reported by this package, in the form the errs
// package expects.
const opReconcile = "sync/records: reconcile"

// Service merges operations into the records they name.
type Service struct {
	works     libraryebook.Repository
	groupings librarycollection.Repository
	filings   librarymembership.Repository
	marks     readingannotation.Repository
	positions readingprogress.Repository
}

// Service satisfies the port the use cases hold.
var _ service.Records = (*Service)(nil)

// Repositories is what the reconciler reaches the replicated records through,
// one per table the node replicates.
//
// It is a struct rather than five parameters because five positional arguments
// of five interface types is an order a caller can get wrong without the
// compiler noticing.
type Repositories struct {
	// Works is library.ebooks.
	Works libraryebook.Repository
	// Groupings is library.collections.
	Groupings librarycollection.Repository
	// Filings is library.ebook_collections.
	Filings librarymembership.Repository
	// Marks is reading.annotations.
	Marks readingannotation.Repository
	// Positions is reading.progress.
	Positions readingprogress.Repository
}

// New returns the reconciler over the repositories of the slices that own the
// replicated records.
func New(repositories *Repositories) *Service {
	return &Service{
		works:     repositories.Works,
		groupings: repositories.Groupings,
		filings:   repositories.Filings,
		marks:     repositories.Marks,
		positions: repositories.Positions,
	}
}

// Reconcile merges one operation into the record it names.
//
// The dispatch is on the entity and nothing else. An entity this node does not
// know is a rejection rather than an error, because it is what a peer running a
// later version legitimately sends: the federation has to survive one of its
// nodes learning a new kind of record, and refusing the operation while
// answering the rest of the batch is what surviving it looks like.
func (s *Service) Reconcile(
	ctx context.Context, op *operation.Operation,
) (operation.Verdict, error) {
	switch op.Target.Entity {
	case operation.TargetEbook:
		return s.reconcileEbook(ctx, op)
	case operation.TargetCollection:
		return s.reconcileCollection(ctx, op)
	case operation.TargetEbookCollection:
		return s.reconcileFiling(ctx, op)
	case operation.TargetAnnotation:
		return s.reconcileAnnotation(ctx, op)
	case operation.TargetReadingProgress:
		return s.reconcilePosition(ctx, op)
	default:
		return operation.Rejected("this node does not replicate " + op.Target.Entity.String()), nil
	}
}

// verdict turns an error raised while resolving or writing a record into
// either a rejection or a failure of the node.
//
// The split is by whose fault it is. An operation naming a record that is not
// here, claiming a field the record cannot hold, or conflicting with something
// already written is a rejection: the batch continues, the caller is told why,
// and no retry will change it. Anything else — a database that stopped
// answering, a connection that went — is the node failing, and the batch has to
// stop rather than report the rest of a reader's history as refused.
func verdict(err error) (operation.Verdict, error) {
	for _, refusal := range []errs.Kind{
		errs.KindNotFound,
		errs.KindInvalidArgument,
		errs.KindAlreadyExists,
		errs.KindFailedPrecondition,
		errs.KindPermissionDenied,
	} {
		if errors.Is(err, refusal) {
			return operation.Rejected(err.Error()), nil
		}
	}

	return operation.Verdict{}, err
}

// notMine is the answer to an operation whose target belongs to another
// reader.
//
// It cannot happen through a device: the log is the reader's own, and the
// record is reached through it. It can happen through a peer, which names the
// reader in the request and could name a record that is not theirs, and a node
// that wrote it would let one authorization reach another reader's collection.
func notMine(entity operation.TargetEntity) (operation.Verdict, error) {
	return operation.Rejected("the " + entity.String() + " belongs to another reader"), nil
}

// missing is the answer to an operation whose target this node does not hold.
//
// Only an insert may create a record, so an update or a deletion for one that
// is not here is refused. That is not a gap in the merge: operations reach a
// node in the order their author wrote them — a device pushes its log in
// order, and a peer is owed its deliveries in the order this node committed
// them — so an update arriving before the insert it depends on means the log
// reaching this node was already broken, and inventing a record out of a
// partial delta would hide that rather than fix it.
func missing(entity operation.TargetEntity) (operation.Verdict, error) {
	return operation.Rejected("this node holds no " + entity.String() + " with that identifier"), nil
}

// rejected wraps a decoding failure, which is always the caller's.
func rejected(err error) (operation.Verdict, error) {
	return operation.Rejected(errs.Wrap(err, errs.KindInvalidArgument, "the change could not be applied").
		WithOp(opReconcile).Error()), nil
}
