package persist

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The client-safe messages attached to a translated driver error.
//
// They are deliberately vague: this package knows the SQLSTATE, not the entity
// the caller was after. A repository that can say something better wraps the
// result again with its own message, and the precise cause — table, column and
// constraint name — stays in the wrapped error, which only the logs see.
const (
	msgNotFound      = "the requested record does not exist"
	msgAlreadyExists = "the record conflicts with one that already exists"
	msgReferences    = "the record references another that does not exist, or is still referenced by one"
	msgInvalid       = "the record does not satisfy the constraints of the database"
	msgConcurrent    = "a concurrent transaction touched the same rows; retry"
	msgUnavailable   = "the database is unavailable"
	msgExhausted     = "the database has no capacity for this request"
	msgDenied        = "the database rejected the credentials of this node"
	msgInternal      = "the database rejected the statement"
)

// Classify translates a driver error into the error vocabulary of the node,
// recording op as the operation that failed.
//
// It is the single place where a SQLSTATE becomes a [errs.Kind], so that every
// slice answers a unique violation with AlreadyExists and a serialization
// failure with a retryable Conflict, without repeating the table anywhere.
//
// A nil error classifies as nil, so a repository can end with
// return Classify(err, op) rather than guarding first.
func Classify(err error, op string) error {
	if err == nil {
		return nil
	}

	// Already classified — by a nested call, or by the caller's own rule.
	// Re-wrapping would bury the more specific kind under a vaguer one.
	var domain *errs.Error
	if errors.As(err, &domain) {
		return err
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return errs.Wrap(err, errs.KindNotFound, msgNotFound).WithOp(op)
	}

	// Cancellation and deadlines cross every layer and are not database
	// faults: reporting them as Internal would fill the logs with alarms about
	// callers that simply hung up.
	switch {
	case errors.Is(err, context.Canceled):
		return errs.Wrap(err, errs.KindCanceled, "the caller gave up before the query finished").WithOp(op)
	case errors.Is(err, context.DeadlineExceeded):
		return errs.Wrap(err, errs.KindDeadlineExceeded, "the query ran past its deadline").WithOp(op)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		kind, message := classifyCode(pgErr.Code)

		return errs.Wrap(err, kind, message).WithOp(op)
	}

	// Anything left is a connection that could not be established or was lost
	// mid-flight: a dial failure, a TLS handshake, a closed pool.
	return errs.Wrap(err, errs.KindUnavailable, msgUnavailable).WithOp(op)
}

// classifyCode maps a SQLSTATE to the kind the transport layer will act on and
// to the text a client may read.
//
// The classes not listed — successful completion, warnings, and the codes a
// correct node cannot provoke — fall through to Internal, which is the honest
// answer: the node emitted a statement the database refused.
func classifyCode(code string) (kind errs.Kind, message string) {
	switch code {
	case pgerrcode.UniqueViolation:
		return errs.KindAlreadyExists, msgAlreadyExists

	case pgerrcode.ForeignKeyViolation, pgerrcode.RestrictViolation:
		return errs.KindFailedPrecondition, msgReferences

	case pgerrcode.NotNullViolation, pgerrcode.CheckViolation, pgerrcode.ExclusionViolation,
		pgerrcode.InvalidTextRepresentation, pgerrcode.StringDataRightTruncationDataException,
		pgerrcode.NumericValueOutOfRange, pgerrcode.DatetimeFieldOverflow,
		pgerrcode.InvalidDatetimeFormat, pgerrcode.DivisionByZero:
		return errs.KindInvalidArgument, msgInvalid

	// Both mean the transaction lost a race it can win by running again, which
	// is why KindConflict is retryable. Manager.Within retries them for the
	// caller.
	case pgerrcode.SerializationFailure, pgerrcode.DeadlockDetected:
		return errs.KindConflict, msgConcurrent

	// A row someone else holds a lock on. Only a statement with NOWAIT can
	// raise it, and such a statement is asking not to wait.
	case pgerrcode.LockNotAvailable:
		return errs.KindConflict, msgConcurrent

	case pgerrcode.TooManyConnections, pgerrcode.ConfigurationLimitExceeded,
		pgerrcode.DiskFull, pgerrcode.OutOfMemory, pgerrcode.ProgramLimitExceeded:
		return errs.KindResourceExhausted, msgExhausted

	case pgerrcode.AdminShutdown, pgerrcode.CrashShutdown, pgerrcode.CannotConnectNow,
		pgerrcode.ConnectionException, pgerrcode.ConnectionFailure,
		pgerrcode.SQLClientUnableToEstablishSQLConnection,
		pgerrcode.SQLServerRejectedEstablishmentOfSQLConnection,
		pgerrcode.OperatorIntervention, pgerrcode.DatabaseDropped,
		pgerrcode.IdleSessionTimeout, pgerrcode.ReadOnlySQLTransaction:
		return errs.KindUnavailable, msgUnavailable

	case pgerrcode.InsufficientPrivilege, pgerrcode.InvalidAuthorizationSpecification,
		pgerrcode.InvalidPassword:
		return errs.KindPermissionDenied, msgDenied

	// Raised when a statement hits statement_timeout, or when the driver
	// cancels one whose context expired. The context checks above catch the
	// common path; this catches the server-side timeout.
	case pgerrcode.QueryCanceled:
		return errs.KindDeadlineExceeded, "the query ran past its deadline"

	default:
		return errs.KindInternal, msgInternal
	}
}

// ConstraintOf returns the name of the constraint the database rejected the
// statement on, or the empty string.
//
// A repository needs it to tell one uniqueness rule from another: a duplicate
// e-mail and a duplicate federated identifier both arrive as a unique
// violation, and only the constraint name says which of the two to report.
func ConstraintOf(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}

	return ""
}

// IsUniqueViolation reports whether err is a uniqueness failure. When
// constraints are given, it also requires the violated constraint to be one of
// them.
func IsUniqueViolation(err error, constraints ...string) bool {
	return hasCode(err, pgerrcode.UniqueViolation, constraints)
}

// IsForeignKeyViolation reports whether err is a referential failure. When
// constraints are given, it also requires the violated constraint to be one of
// them.
func IsForeignKeyViolation(err error, constraints ...string) bool {
	return hasCode(err, pgerrcode.ForeignKeyViolation, constraints)
}

// IsNoRows reports whether err is the absence of a row rather than a failure.
// It sees through classification, so it holds both for the driver error and
// for what [Classify] made of it.
func IsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errs.KindNotFound)
}

// hasCode reports whether err carries the given SQLSTATE and, when the list is
// not empty, one of the named constraints.
func hasCode(err error, code string, constraints []string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		return false
	}

	if len(constraints) == 0 {
		return true
	}

	return slices.Contains(constraints, pgErr.ConstraintName)
}
