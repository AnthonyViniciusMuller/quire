// Package authn is the interceptor that turns an access token into an identity,
// and the context key that identity travels under.
//
// It lives in the identity slice because this is the only part of the node that
// holds the signing key, which is what internal/shared/grpcx says when it
// documents the chain it does not include: nothing there knows what a reader is.
//
// Two decisions shape it.
//
// It denies by default. A method that is not named as public needs a token, so
// a call added to the contract is authenticated until somebody decides
// otherwise — which is the direction the mistake should go in.
//
// It reads the token and nothing else. It does not ask the database whether the
// reader still exists or whether the device is still bound, because an access
// token is not revocable by construction (RNF11): it is verified by signature
// against keys anybody can fetch, which is what lets the service mesh validate
// it too (RNF12). What bounds a revoked device is the lifetime of the token it
// is holding, and revocation acts on the refresh credential it would need in
// order to get another.
package authn

import (
	"context"
	"log/slog"
	"strings"
	"uuid"

	middleware "github.com/grpc-ecosystem/go-grpc-middleware/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// opIntercept is the operation reported by this file, in the form the errs
// package expects.
const opIntercept = "identity/authn: intercept"

// The stable machine-readable codes this interceptor raises.
const (
	// CodeNoToken is a call that presented no credential at all.
	CodeNoToken = "no_token"
	// CodeMalformedToken is an authorization header this node cannot read: the
	// wrong scheme, or nothing after it.
	CodeMalformedToken = "malformed_token"
)

// authorizationHeader is the metadata key a bearer token travels under, and
// bearerScheme is what precedes it. Both are the names every gRPC client
// library already uses.
const (
	authorizationHeader = "authorization"
	bearerScheme        = "bearer"
)

// reflectionPrefix is the service the server publishes for grpcurl and ghz, and
// only outside production.
//
// It is public here without a condition, because a method that is not
// registered never reaches an interceptor: the server answers an unknown one
// with Unimplemented before the chain runs. So in production, where reflection
// is not registered, this line describes nothing.
const reflectionPrefix = "/grpc.reflection."

// identityKey addresses the identity in a context.
type identityKey struct{}

// Identity is who a request is from, as the token asserts it.
type Identity struct {
	// UserID is the reader.
	UserID uuid.UUID
	// DeviceID is the appliance the session belongs to. RN10 checks a
	// synchronization operation against it, and it is why the token names a
	// device at all.
	DeviceID uuid.UUID
	// TokenID is the token's own identifier, which makes one session's path
	// through the logs followable.
	TokenID uuid.UUID
}

// From returns the identity a request is being served under, and whether there
// is one. A public method has none.
func From(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(Identity)

	return identity, ok
}

// Require returns the identity, or the error a handler should pass on when
// there is none.
//
// A handler on an authenticated method calls this and does not check the flag:
// reaching it without an identity means the method was left out of the public
// set and the interceptor let it through, which is a fault of this node rather
// than of the caller.
func Require(ctx context.Context) (Identity, error) {
	identity, ok := From(ctx)
	if !ok {
		return Identity{}, errs.New(errs.KindInternal, "the node could not identify the caller").
			WithOp(opIntercept)
	}

	return identity, nil
}

// Interceptor verifies access tokens.
type Interceptor struct {
	auth   service.AuthService
	clock  service.Clock
	public map[string]struct{}
}

// New returns the interceptor over the token service, treating the methods of
// public as needing no credential.
func New(auth service.AuthService, clock service.Clock, public []string) *Interceptor {
	set := make(map[string]struct{}, len(public))
	for _, method := range public {
		set[method] = struct{}{}
	}

	return &Interceptor{auth: auth, clock: clock, public: set}
}

// PublicMethods are the calls of the contract that answer a caller who has no
// session, which is the whole of what the specification exempts: UC07 (logging
// in and out), UC08 (recovering a password) and UC14 (binding to an origin
// server).
//
// Refreshing is here for a reason of its own. It authenticates itself with the
// credential it presents, and requiring an access token as well would make the
// shorter lifetime the shorter session — a device whose token has expired is
// exactly the device that needs this call.
func PublicMethods() []string {
	return []string{
		quirev1.AuthService_RegisterUser_FullMethodName,
		quirev1.AuthService_Login_FullMethodName,
		quirev1.AuthService_Logout_FullMethodName,
		quirev1.AuthService_RefreshSession_FullMethodName,
		quirev1.AuthService_RequestPasswordRecovery_FullMethodName,
		quirev1.AuthService_ResetPassword_FullMethodName,
	}
}

// Unary verifies the token of a unary call and stamps the identity into the
// context.
func (i *Interceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		authenticated, err := i.authenticate(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}

		return handler(authenticated, req)
	}
}

// Stream is [Interceptor.Unary] for a streaming method, which the
// synchronization service is made of.
func (i *Interceptor) Stream() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		authenticated, err := i.authenticate(stream.Context(), info.FullMethod)
		if err != nil {
			return err
		}

		wrapped := middleware.WrapServerStream(stream)
		wrapped.WrappedContext = authenticated

		return handler(srv, wrapped)
	}
}

// authenticate returns the context the rest of the call is served under.
func (i *Interceptor) authenticate(ctx context.Context, method string) (context.Context, error) {
	if i.isPublic(method) {
		return ctx, nil
	}

	token, err := bearerToken(ctx)
	if err != nil {
		return nil, err
	}

	claims, err := i.auth.VerifyAccess(token, i.clock.Now())
	if err != nil {
		return nil, err
	}

	identity := Identity{UserID: claims.UserID, DeviceID: claims.DeviceID, TokenID: claims.TokenID}

	// Recorded on the context rather than on this record alone, so that
	// everything logged below — down to a repository that has no idea which
	// method it serves — carries who it was for.
	ctx = logging.WithAttrs(ctx,
		slog.String(logging.KeyUserID, identity.UserID.String()),
		slog.String(logging.KeyDeviceID, identity.DeviceID.String()))

	return context.WithValue(ctx, identityKey{}, identity), nil
}

// isPublic reports whether method answers a caller with no session.
func (i *Interceptor) isPublic(method string) bool {
	if _, found := i.public[method]; found {
		return true
	}

	return strings.HasPrefix(method, reflectionPrefix)
}

// bearerToken returns the token the caller presented.
func bearerToken(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", missing()
	}

	values := md.Get(authorizationHeader)
	if len(values) == 0 {
		return "", missing()
	}

	scheme, token, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, bearerScheme) {
		return "", errs.New(errs.KindUnauthenticated, "the credential is not a bearer token").
			WithOp(opIntercept).
			WithCode(CodeMalformedToken)
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return "", missing()
	}

	return token, nil
}

// missing is the answer to a call that presented nothing.
func missing() error {
	return errs.New(errs.KindUnauthenticated, "this call needs an access token").
		WithOp(opIntercept).
		WithCode(CodeNoToken)
}
