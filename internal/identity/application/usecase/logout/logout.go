// Package logout is the second half of UC07: it consumes the refresh credential
// presented, which ends the session of the device that holds it and of no other
// device.
//
// That last part is RN07 — a reader uses several appliances at once — and it is
// why the call takes a credential rather than a reader: ending every session of
// an account is a different act, and belongs to revoking a device or resetting a
// password.
//
// The call succeeds for a credential it does not recognize. That is deliberate,
// and it is the rule RFC 7009 states for token revocation: an invalid token
// causes no error response, because the client cannot do anything reasonable
// with one. What the caller asked for is that this credential stop working, and
// a credential that never worked already satisfies it — while a device retrying
// after a lost reply would otherwise be told its own successful logout failed.
package logout

import (
	"context"
	"errors"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "identity/logout: execute"

// CodeNoCredential is a request that presents nothing. It is a malformed
// request rather than a failed revocation, and unlike an unrecognized
// credential it says nothing about any session.
//
//nolint:gosec // G101: this is the name of an error, not a credential.
const CodeNoCredential = "no_credential"

// Logout ends sessions.
type Logout struct {
	credentials credential.Repository
	auth        service.AuthService
}

// Logout satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*Logout)(nil)

// New returns the use case over its dependencies.
func New(credentials credential.Repository, auth service.AuthService) *Logout {
	return &Logout{credentials: credentials, auth: auth}
}

// Execute ends the session the credential belongs to.
func (l *Logout) Execute(ctx context.Context, input Input) (Output, error) {
	if input.RefreshToken == "" {
		return Output{}, errs.New(errs.KindInvalidArgument, "the request presents no credential").
			WithOp(opExecute).
			WithCode(CodeNoCredential).
			WithField("refresh_token", "it must carry the credential whose session is to end")
	}

	// Only the digest is compared, because only the digest is stored.
	issued, err := l.credentials.GetByTokenHash(ctx, l.auth.DigestOf(input.RefreshToken))
	if errors.Is(err, errs.KindNotFound) {
		return Output{}, nil
	}

	if err != nil {
		return Output{}, err
	}

	// A password recovery credential is not a session, and presenting one here
	// must not spend it: that would turn a call anybody may make into a way to
	// cancel somebody's recovery.
	if issued.Kind != credential.KindSessionRefresh {
		return Output{}, nil
	}

	err = l.credentials.Consume(ctx, issued.ID)
	if err != nil && !errors.Is(err, errs.KindConflict) {
		return Output{}, err
	}

	// A conflict means it had already been spent, which is the outcome asked
	// for.
	return Output{}, nil
}
