// Package login is the first half of UC07: it verifies the password, binds the
// calling device if it is not bound yet, and issues the session.
//
// Two properties are worth stating before the code, because both are easy to
// lose and neither is visible from a passing test.
//
// The call costs the same whether or not the reader exists. A lookup that
// missed and returned early would answer in a millisecond while a real one
// takes the hashing cost, and the difference is an oracle for which accounts
// are registered here — local names being guessable by construction, since RN09
// puts one inside every identifier. So the password is always checked, against
// the digest that nothing matches when there is nobody to check it against.
//
// A device that is presented is proved to belong to the reader who just
// authenticated, and one that does not is answered exactly as one that does not
// exist. Otherwise the reply would tell a reader which device ids belong to
// somebody else.
package login

import (
	"context"
	"errors"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "identity/login: execute"

// The stable machine-readable codes this use case raises.
const (
	// CodeInvalidCredentials is the one answer to everything about the reader
	// and the password: no such reader, a reader this node only replicates, and
	// a wrong password are indistinguishable on purpose.
	//
	//nolint:gosec // G101: this is the name of an error, not a credential.
	CodeInvalidCredentials = "invalid_credentials"
	// CodeDeviceRevoked is an appliance whose binding was ended. Quadro 17 is
	// explicit that an inactive device may not renew its credentials, and
	// issuing it a session would be renewing them.
	CodeDeviceRevoked = "device_revoked"
	// CodeNoReaderNamed is a request that names neither a local name nor an
	// address. It is a malformed request rather than a failed authentication,
	// and saying so tells a client with a bug what is wrong without saying
	// anything about any account.
	CodeNoReaderNamed = "no_reader_named"
)

// Login starts sessions.
type Login struct {
	users       user.Repository
	devices     device.Repository
	credentials credential.Repository
	hasher      service.HashService
	auth        service.AuthService
	localServer service.LocalServer
	clock       service.Clock
	transaction service.Transaction
}

// Login satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*Login)(nil)

// New returns the use case over its dependencies.
func New(
	users user.Repository,
	devices device.Repository,
	credentials credential.Repository,
	hasher service.HashService,
	auth service.AuthService,
	localServer service.LocalServer,
	clock service.Clock,
	transaction service.Transaction,
) *Login {
	return &Login{
		users:       users,
		devices:     devices,
		credentials: credentials,
		hasher:      hasher,
		auth:        auth,
		localServer: localServer,
		clock:       clock,
		transaction: transaction,
	}
}

// Execute authenticates the reader and issues the session.
//
// The binding and the issuing are one unit of work. A node that bound a device
// and then failed to store its credential would have attached an appliance to
// an account that cannot use it: a row nobody would think to look for, holding
// an identifier that every later vector clock would be keyed by.
//
//nolint:gocritic // hugeParam: the Usecase interface fixes this signature by value.
func (l *Login) Execute(ctx context.Context, input Input) (Output, error) {
	reader, err := l.authenticate(ctx, &input)
	if err != nil {
		return Output{}, err
	}

	var output Output

	err = l.transaction.Within(ctx, func(ctx context.Context) error {
		appliance, bindErr := l.bind(ctx, reader, input.Device)
		if bindErr != nil {
			return bindErr
		}

		session, issueErr := l.issue(ctx, reader, appliance)
		if issueErr != nil {
			return issueErr
		}

		federatedID, idErr := reader.FederatedID(l.localServer.Domain())
		if idErr != nil {
			return idErr
		}

		output = Output{Session: session, User: reader, Device: appliance, FederatedID: federatedID}

		return nil
	})
	if err != nil {
		return Output{}, err
	}

	return output, nil
}

// authenticate returns the reader the request names, having checked their
// password.
//
// The hashing runs whether or not there is a reader to check, which is what
// makes the call cost the same either way.
func (l *Login) authenticate(ctx context.Context, input *Input) (*user.User, error) {
	reader, err := l.lookup(ctx, input)
	if err != nil {
		return nil, err
	}

	// A reader this node only replicates has no password (C03), and RN08 gives
	// authentication to their origin server. They are answered as nobody.
	digest := l.hasher.AbsentDigest()
	if reader != nil && reader.Authenticates() {
		digest = reader.PasswordHash
	}

	matched, err := l.hasher.Verify(input.Password, digest)
	if err != nil {
		return nil, err
	}

	if !matched || reader == nil || !reader.Authenticates() {
		return nil, errs.New(errs.KindUnauthenticated, "that name and password do not match").
			WithOp(opExecute).
			WithCode(CodeInvalidCredentials)
	}

	return reader, nil
}

// lookup finds the reader the request names, or reports nobody.
//
// A name that is not a valid identifier and a name that is valid but registered
// to nobody both come back as nobody, rather than one of them as a rejected
// argument. The two are the same fact to whoever asked — there is no such
// account — and answering them differently would separate the names this node
// could hold from the ones it does hold.
func (l *Login) lookup(ctx context.Context, input *Input) (*user.User, error) {
	if input.LocalName == "" && input.Email == "" {
		return nil, errs.New(errs.KindInvalidArgument, "the request names no reader").
			WithOp(opExecute).
			WithCode(CodeNoReaderNamed).
			WithField("login_id", "it must carry either a local name or an e-mail address")
	}

	originServerID, err := l.localServer.ID(ctx)
	if err != nil {
		return nil, err
	}

	var reader *user.User

	if input.Email != "" {
		email, parseErr := user.ParseEmail(input.Email)
		if parseErr != nil {
			//nolint:nilerr,nilnil // a malformed address names nobody, which is not a failure of this call.
			return nil, nil
		}

		reader, err = l.users.GetByEmail(ctx, originServerID, email)
	} else {
		localName, parseErr := user.ParseLocalName(input.LocalName)
		if parseErr != nil {
			//nolint:nilerr,nilnil // a malformed name names nobody, which is not a failure of this call.
			return nil, nil
		}

		reader, err = l.users.GetByLocalName(ctx, originServerID, localName)
	}

	if errors.Is(err, errs.KindNotFound) {
		//nolint:nilnil // nobody by that name, which is not a failure of this call.
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return reader, nil
}

// bind returns the device the session is for, binding it if it is new.
func (l *Login) bind(ctx context.Context, reader *user.User, binding Binding) (*device.Device, error) {
	if binding.DeviceID == "" {
		return l.create(ctx, reader, binding)
	}

	id, err := uuid.Parse(binding.DeviceID)
	if err != nil {
		return nil, unknownDevice()
	}

	appliance, err := l.devices.GetByID(ctx, id)
	if errors.Is(err, errs.KindNotFound) {
		return nil, unknownDevice()
	}

	if err != nil {
		return nil, err
	}

	// Answered exactly as an unknown device would be. A reply that
	// distinguished them would tell this reader which identifiers belong to
	// somebody else.
	if appliance.UserID != reader.ID {
		return nil, unknownDevice()
	}

	if !appliance.Active {
		return nil, errs.New(errs.KindPermissionDenied, "that device has been unbound from this account").
			WithOp(opExecute).
			WithCode(CodeDeviceRevoked)
	}

	// The name and platform the request carries are ignored for an appliance
	// that is already bound. Renaming a device is UC06's business and has its
	// own call; doing it here would let a login quietly rewrite a record the
	// reader is using to recognize their own devices.
	return appliance, nil
}

// create binds an appliance being seen for the first time.
func (l *Login) create(ctx context.Context, reader *user.User, binding Binding) (*device.Device, error) {
	name, err := device.ParseName(binding.Name)
	if err != nil {
		return nil, err
	}

	platform, err := device.ParsePlatform(binding.Platform)
	if err != nil {
		return nil, err
	}

	appliance, err := device.New(reader.ID, name, platform)
	if err != nil {
		return nil, err
	}

	err = l.devices.Create(ctx, appliance)
	if err != nil {
		return nil, err
	}

	return appliance, nil
}

// issue signs the access token and stores the credential that outlives it.
func (l *Login) issue(
	ctx context.Context,
	reader *user.User,
	appliance *device.Device,
) (service.Session, error) {
	now := l.clock.Now()

	accessToken, claims, err := l.auth.IssueAccess(reader.ID, appliance.ID, now)
	if err != nil {
		return service.Session{}, err
	}

	refresh, err := l.auth.IssueRefresh(now)
	if err != nil {
		return service.Session{}, err
	}

	issued, err := credential.NewSession(reader.ID, appliance.ID, refresh.Digest, refresh.ExpiresAt)
	if err != nil {
		return service.Session{}, err
	}

	err = l.credentials.Create(ctx, issued)
	if err != nil {
		return service.Session{}, err
	}

	return service.Session{
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  claims.ExpiresAt,
		RefreshToken:          refresh.Value,
		RefreshTokenExpiresAt: refresh.ExpiresAt,
	}, nil
}

// unknownDevice is the answer to a device identifier this reader has not bound,
// whatever the reason.
func unknownDevice() error {
	return errs.New(errs.KindNotFound, "that device is not bound to this account").
		WithOp(opExecute).
		WithCode(device.CodeNotFound)
}
