package persist_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// op is the operation the tests classify under.
const op = "identity/user: register"

// pgError builds the driver error the server would have returned for code,
// with the detail a real one carries.
func pgError(code, constraint string) *pgconn.PgError {
	return &pgconn.PgError{
		Code:           code,
		Severity:       "ERROR",
		Message:        `duplicate key value violates unique constraint "` + constraint + `"`,
		Detail:         `Key (email)=(reader@quire-a.example) already exists.`,
		SchemaName:     "identity",
		TableName:      "users",
		ConstraintName: constraint,
	}
}

func TestClassifyPassesNilThrough(t *testing.T) {
	t.Parallel()

	if err := persist.Classify(nil, op); err != nil {
		t.Errorf("Classify(nil) = %v, want nil", err)
	}
}

func TestClassifyMapsSQLStatesToKinds(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		code string
		want errs.Kind
	}{
		"unique violation":      {code: "23505", want: errs.KindAlreadyExists},
		"foreign key violation": {code: "23503", want: errs.KindFailedPrecondition},
		"not null violation":    {code: "23502", want: errs.KindInvalidArgument},
		"check violation":       {code: "23514", want: errs.KindInvalidArgument},
		"invalid text":          {code: "22P02", want: errs.KindInvalidArgument},
		"serialization failure": {code: "40001", want: errs.KindConflict},
		"deadlock detected":     {code: "40P01", want: errs.KindConflict},
		"too many connections":  {code: "53300", want: errs.KindResourceExhausted},
		"admin shutdown":        {code: "57P01", want: errs.KindUnavailable},
		"insufficient rights":   {code: "42501", want: errs.KindPermissionDenied},
		"query canceled":        {code: "57014", want: errs.KindDeadlineExceeded},
		"undefined table":       {code: "42P01", want: errs.KindInternal},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := persist.Classify(pgError(test.code, "users_email_key"), op)

			if got := errs.KindOf(err); got != test.want {
				t.Errorf("kind = %s, want %s", got, test.want)
			}

			if !errors.Is(err, test.want) {
				t.Errorf("errors.Is(err, %s) is false", test.want)
			}
		})
	}
}

func TestClassifyMakesLostRacesRetryable(t *testing.T) {
	t.Parallel()

	// The replication worker decides between backing off and giving up on
	// exactly this, so a serialization failure must not arrive as terminal.
	err := persist.Classify(pgError("40001", ""), op)

	if !errs.Retryable(err) {
		t.Errorf("a serialization failure classified as %s, which is not retryable", errs.KindOf(err))
	}
}

func TestClassifyMapsMissingRowsToNotFound(t *testing.T) {
	t.Parallel()

	err := persist.Classify(fmt.Errorf("scanning user: %w", pgx.ErrNoRows), op)

	if got := errs.KindOf(err); got != errs.KindNotFound {
		t.Errorf("kind = %s, want %s", got, errs.KindNotFound)
	}

	if !persist.IsNoRows(err) {
		t.Error("IsNoRows is false for a classified pgx.ErrNoRows")
	}
}

func TestClassifyKeepsCancellationApart(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err  error
		want errs.Kind
	}{
		"canceled": {err: context.Canceled, want: errs.KindCanceled},
		"deadline": {err: context.DeadlineExceeded, want: errs.KindDeadlineExceeded},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A caller that hung up is not a database fault, and must not be
			// reported as one.
			if got := errs.KindOf(persist.Classify(test.err, op)); got != test.want {
				t.Errorf("kind = %s, want %s", got, test.want)
			}
		})
	}
}

func TestClassifyTreatsAnUnrecognizedErrorAsUnavailable(t *testing.T) {
	t.Parallel()

	// Whatever is neither a server error nor a cancellation is a connection
	// that could not be established or was lost, which is worth retrying.
	err := persist.Classify(errors.New("dial tcp 10.0.0.1:5432: connect: connection refused"), op)

	if got := errs.KindOf(err); got != errs.KindUnavailable {
		t.Errorf("kind = %s, want %s", got, errs.KindUnavailable)
	}
}

func TestClassifyLeavesAnAlreadyClassifiedErrorAlone(t *testing.T) {
	t.Parallel()

	// A repository that recognized the constraint reports something more
	// precise than this package can. Re-wrapping would bury it.
	precise := errs.New(errs.KindAlreadyExists, "that e-mail is already registered").
		WithCode("email_already_registered")

	err := persist.Classify(fmt.Errorf("inserting user: %w", precise), op)

	if got := errs.CodeOf(err); got != "email_already_registered" {
		t.Errorf("code = %q, want %q", got, "email_already_registered")
	}
}

func TestClassifyRecordsTheOperation(t *testing.T) {
	t.Parallel()

	err := persist.Classify(pgError("23505", "users_email_key"), op)

	if !strings.Contains(err.Error(), op) {
		t.Errorf("the error does not name the operation: %v", err)
	}
}

func TestClassifyKeepsTheDatabaseOutOfTheClientMessage(t *testing.T) {
	t.Parallel()

	err := persist.Classify(pgError("23505", "users_email_key"), op)

	// The constraint, the table and the offending value belong in the log
	// line, which reads the wrapped cause, and never in the answer.
	message := errs.MessageOf(err)
	for _, internal := range []string{"users", "identity", "users_email_key", "reader@quire-a.example"} {
		if strings.Contains(message, internal) {
			t.Errorf("the client message names %q: %q", internal, message)
		}
	}

	if !strings.Contains(err.Error(), "users_email_key") {
		t.Errorf("the log rendering lost the constraint name: %v", err)
	}
}

func TestConstraintOfNamesTheViolatedRule(t *testing.T) {
	t.Parallel()

	err := persist.Classify(pgError("23505", "users_local_name_server_name_key"), op)

	if got, want := persist.ConstraintOf(err), "users_local_name_server_name_key"; got != want {
		t.Errorf("ConstraintOf = %q, want %q", got, want)
	}

	if got := persist.ConstraintOf(errors.New("boom")); got != "" {
		t.Errorf("ConstraintOf = %q, want the empty string", got)
	}
}

func TestIsUniqueViolationFiltersByConstraint(t *testing.T) {
	t.Parallel()

	err := persist.Classify(pgError("23505", "users_email_key"), op)

	if !persist.IsUniqueViolation(err) {
		t.Error("IsUniqueViolation is false for a unique violation")
	}

	// A duplicate e-mail and a duplicate federated identifier arrive as the
	// same SQLSTATE; only the constraint name tells them apart.
	if !persist.IsUniqueViolation(err, "users_local_name_server_name_key", "users_email_key") {
		t.Error("IsUniqueViolation is false for the constraint that was violated")
	}

	if persist.IsUniqueViolation(err, "users_local_name_server_name_key") {
		t.Error("IsUniqueViolation is true for a constraint that was not violated")
	}

	if persist.IsUniqueViolation(persist.Classify(pgError("23503", "devices_user_id_fkey"), op)) {
		t.Error("IsUniqueViolation is true for a foreign key violation")
	}
}

func TestIsForeignKeyViolationFiltersByConstraint(t *testing.T) {
	t.Parallel()

	err := persist.Classify(pgError("23503", "devices_user_id_fkey"), op)

	if !persist.IsForeignKeyViolation(err, "devices_user_id_fkey") {
		t.Error("IsForeignKeyViolation is false for the constraint that was violated")
	}

	if persist.IsForeignKeyViolation(err, "annotations_ebook_id_fkey") {
		t.Error("IsForeignKeyViolation is true for a constraint that was not violated")
	}
}
