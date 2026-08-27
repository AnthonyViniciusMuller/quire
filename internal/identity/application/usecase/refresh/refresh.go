// Package refresh exchanges a refresh credential for a new session.
//
// The access token is short by RNF11, so this is the call a device makes most
// often after the synchronization ones, and what it does is a rotation rather
// than a renewal: the credential presented is consumed and the reply carries its
// replacement. D07 in docs/tcc-corrections.md is the argument, and the second
// half of it is here too — a credential presented after it has been consumed is
// by construction a credential two parties hold, since the legitimate device
// already exchanged it and holds the replacement. The node answers by ending
// every session of that device.
//
// The cost of that answer is stated where it is paid: a device whose reply was
// lost on a mobile network retries with the credential it still has and is
// logged out for it. The alternative is a stolen credential that refreshes
// alongside the reader for as long as the reader does.
package refresh

import (
	"context"
	"errors"
	"log/slog"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "identity/refresh: execute"

// The stable machine-readable codes this use case raises.
const (
	// CodeNoCredential is a request that presents nothing.
	//
	//nolint:gosec // G101: this is the name of an error, not a credential.
	CodeNoCredential = "no_credential"
	// CodeInvalidCredential is a credential this node did not issue, did not
	// issue as a session, or issued and has since expired. The three are one
	// answer: which of them it was is a fact about somebody else's session.
	//
	//nolint:gosec // G101: this is the name of an error, not a credential.
	CodeInvalidCredential = "invalid_credential"
	// CodeCredentialReused is a credential presented after it was spent, and
	// the sessions of the device it belonged to have been ended.
	//
	//nolint:gosec // G101: this is the name of an error, not a credential.
	CodeCredentialReused = "credential_reused"
	// CodeDeviceRevoked is an appliance whose binding was ended. Quadro 17
	// makes this the rule the flag exists for: an inactive device may not renew
	// its credentials.
	CodeDeviceRevoked = "device_revoked"
)

// Refresh replaces sessions.
type Refresh struct {
	credentials credential.Repository
	devices     device.Repository
	auth        service.AuthService
	clock       service.Clock
	transaction service.Transaction
}

// Refresh satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*Refresh)(nil)

// New returns the use case over its dependencies.
func New(
	credentials credential.Repository,
	devices device.Repository,
	auth service.AuthService,
	clock service.Clock,
	transaction service.Transaction,
) *Refresh {
	return &Refresh{
		credentials: credentials,
		devices:     devices,
		auth:        auth,
		clock:       clock,
		transaction: transaction,
	}
}

// Execute spends the credential presented and issues its replacement.
//
// The unit of work holds the spending and the issuing, and nothing before them.
// That boundary is not a detail: the reuse path revokes the device's sessions
// and then returns an error, so inside a unit of work the revocation would be
// rolled back by the very error it was raised with — the node would report the
// reuse and forget it in the same breath. Establishing what was presented
// happens outside, where its writes commit on their own.
//
// Spending is safe outside a unit too, because the statement that spends
// refuses one already spent: two devices presenting the same credential at the
// same instant cannot both be answered with a session, whatever they read
// first.
func (r *Refresh) Execute(ctx context.Context, input Input) (Output, error) {
	if input.RefreshToken == "" {
		return Output{}, errs.New(errs.KindInvalidArgument, "the request presents no credential").
			WithOp(opExecute).
			WithCode(CodeNoCredential).
			WithField("refresh_token", "it must carry the credential the session is renewed with")
	}

	presented, err := r.present(ctx, input.RefreshToken)
	if err != nil {
		return Output{}, err
	}

	var output Output

	// The spending and the issuing are one: a node that spent the credential
	// and then failed to store its replacement would have ended a session and
	// handed back nothing.
	err = r.transaction.Within(ctx, func(ctx context.Context) error {
		if consumeErr := r.credentials.Consume(ctx, presented.ID); consumeErr != nil {
			return consumeErr
		}

		session, issueErr := r.issue(ctx, presented)
		if issueErr != nil {
			return issueErr
		}

		output = Output{Session: session}

		return nil
	})
	if err != nil {
		return Output{}, err
	}

	return output, nil
}

// present returns the credential the caller holds, having established that it
// is one this node issued as a session, that it has not been spent, and that
// the device it belongs to may still renew.
func (r *Refresh) present(ctx context.Context, token string) (*credential.Credential, error) {
	issued, err := r.credentials.GetByTokenHash(ctx, r.auth.DigestOf(token))
	if errors.Is(err, errs.KindNotFound) {
		return nil, invalidCredential()
	}

	if err != nil {
		return nil, err
	}

	// A password recovery credential is not a session. Exchanging one here
	// would turn the weaker credential of UC08 into the stronger one of UC07.
	if issued.Kind != credential.KindSessionRefresh {
		return nil, invalidCredential()
	}

	if issued.Consumed {
		return nil, r.reused(ctx, issued)
	}

	if issued.Expired(r.clock.Now()) {
		return nil, invalidCredential()
	}

	appliance, err := r.devices.GetByID(ctx, issued.DeviceID)
	if err != nil {
		return nil, err
	}

	if !appliance.Active {
		return nil, errs.New(errs.KindPermissionDenied, "that device has been unbound from this account").
			WithOp(opExecute).
			WithCode(CodeDeviceRevoked)
	}

	return issued, nil
}

// reused ends every session of the device the spent credential belonged to, and
// reports what happened.
//
// Two parties hold a credential that was already exchanged, and this node
// cannot tell which of them is the reader: the legitimate device holds the
// replacement, so whoever is presenting the old one is either a copy or a device
// whose reply was lost. Ending both sessions is the answer to the first and a
// re-authentication for the second.
func (r *Refresh) reused(ctx context.Context, issued *credential.Credential) error {
	// Worth a record even though the caller is told: this is the one event in
	// the slice that says a credential exists in two places, and an operator
	// looking at a reader's complaint needs to see which device it was.
	logging.From(ctx).WarnContext(ctx, "a spent refresh credential was presented, ending the device's sessions",
		slog.String(logging.KeyUserID, issued.UserID.String()),
		slog.String(logging.KeyDeviceID, issued.DeviceID.String()))

	err := r.credentials.ConsumeForDevice(ctx, issued.DeviceID)
	if err != nil {
		return err
	}

	return errs.New(errs.KindUnauthenticated,
		"that credential has already been used, and the sessions of the device it belonged to have ended").
		WithOp(opExecute).
		WithCode(CodeCredentialReused)
}

// issue signs the new access token and stores the credential that replaces the
// one just spent.
func (r *Refresh) issue(ctx context.Context, presented *credential.Credential) (service.Session, error) {
	now := r.clock.Now()

	accessToken, claims, err := r.auth.IssueAccess(presented.UserID, presented.DeviceID, now)
	if err != nil {
		return service.Session{}, err
	}

	secret, err := r.auth.IssueRefresh(now)
	if err != nil {
		return service.Session{}, err
	}

	replacement, err := credential.NewSession(presented.UserID, presented.DeviceID, secret.Digest, secret.ExpiresAt)
	if err != nil {
		return service.Session{}, err
	}

	err = r.credentials.Create(ctx, replacement)
	if err != nil {
		return service.Session{}, err
	}

	return service.Session{
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  claims.ExpiresAt,
		RefreshToken:          secret.Value,
		RefreshTokenExpiresAt: secret.ExpiresAt,
	}, nil
}

// invalidCredential is the one answer to every credential this node will not
// exchange, short of one it recognizes as reused.
func invalidCredential() error {
	return errs.New(errs.KindUnauthenticated, "that credential is not valid").
		WithOp(opExecute).
		WithCode(CodeInvalidCredential)
}
