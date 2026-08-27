package authn_test

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// fixture is the interceptor and the token service its tokens come from.
type fixture struct {
	interceptor *authn.Interceptor
	auth        *apptest.AuthService
}

func newFixture() fixture {
	auth := apptest.NewAuthService()

	return fixture{
		interceptor: authn.New(auth, apptest.NewClock(now()), authn.PublicMethods()),
		auth:        auth,
	}
}

// call runs the interceptor over method, with whatever metadata the caller
// supplied, and reports the context the handler was reached with.
func (f *fixture) call(t *testing.T, method string, md metadata.MD) (context.Context, error) {
	t.Helper()

	ctx := t.Context()
	if md != nil {
		ctx = metadata.NewIncomingContext(ctx, md)
	}

	var served context.Context

	_, err := f.interceptor.Unary()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method},
		func(ctx context.Context, _ any) (any, error) {
			served = ctx

			return nil, nil //nolint:nilnil // the handler under test returns nothing.
		})

	return served, err
}

// bearer is the metadata a device sends.
func bearer(token string) metadata.MD {
	return metadata.Pairs("authorization", "Bearer "+token)
}

func TestUnaryStampsTheIdentity(t *testing.T) {
	t.Parallel()

	f := newFixture()
	userID, deviceID := uuid.New(), uuid.New()

	token, claims, err := f.auth.IssueAccess(userID, deviceID, now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	served, err := f.call(t, quirev1.AuthService_GetUser_FullMethodName, bearer(token))
	if err != nil {
		t.Fatalf("the interceptor refused a valid token: %v", err)
	}

	identity, ok := authn.From(served)
	if !ok {
		t.Fatal("the handler was reached without an identity")
	}

	switch {
	case identity.UserID != userID:
		t.Error("the identity names another reader")
	case identity.DeviceID != deviceID:
		t.Error("the identity names no device, and RN10 checks an operation against it")
	case identity.TokenID != claims.TokenID:
		t.Error("the identity carries another token's identifier")
	}
}

// TestUnaryDeniesByDefault is the direction the mistake has to go in: a method
// nobody named as public needs a token.
func TestUnaryDeniesByDefault(t *testing.T) {
	t.Parallel()

	f := newFixture()

	authenticated := []string{
		quirev1.AuthService_GetUser_FullMethodName,
		quirev1.AuthService_UpdateUser_FullMethodName,
		quirev1.AuthService_ChangePassword_FullMethodName,
		quirev1.AuthService_DeleteUser_FullMethodName,
		quirev1.AuthService_RegisterDevice_FullMethodName,
		quirev1.AuthService_ListDevices_FullMethodName,
		quirev1.AuthService_UpdateDevice_FullMethodName,
		quirev1.AuthService_RevokeDevice_FullMethodName,
		// A method of a service this slice has never heard of, standing in for
		// the ones the later slices register.
		"/quire.v1.LibraryService/ListEbooks",
	}

	for _, method := range authenticated {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			served, err := f.call(t, method, nil)
			if err == nil {
				t.Fatal("the interceptor let an unauthenticated call through")
			}

			if !errors.Is(err, errs.KindUnauthenticated) {
				t.Errorf("error = %v, want unauthenticated", err)
			}

			if served != nil {
				t.Error("the handler was reached")
			}
		})
	}
}

// TestUnaryLetsThePublicMethodsThrough covers what the specification exempts:
// UC07, UC08 and UC14 answer a caller who has no session.
func TestUnaryLetsThePublicMethodsThrough(t *testing.T) {
	t.Parallel()

	f := newFixture()

	for _, method := range authn.PublicMethods() {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			served, err := f.call(t, method, nil)
			if err != nil {
				t.Fatalf("the interceptor refused a public method: %v", err)
			}

			if served == nil {
				t.Fatal("the handler was not reached")
			}

			if _, ok := authn.From(served); ok {
				t.Error("a public method was served with an identity nobody proved")
			}
		})
	}
}

// TestPublicMethodsAreTheOnesTheSpecificationExempts pins the set itself, so
// that a method added to it later is a deliberate act rather than a diff nobody
// read.
func TestPublicMethodsAreTheOnesTheSpecificationExempts(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		// UC14, and the UC13 it includes.
		quirev1.AuthService_RegisterUser_FullMethodName: true,
		// UC07, both halves. Logging out presents the refresh credential, so a
		// device whose access token has expired can still end its session.
		quirev1.AuthService_Login_FullMethodName:  true,
		quirev1.AuthService_Logout_FullMethodName: true,
		// Authenticated by the credential it presents. Requiring a token as
		// well would make the shorter lifetime the shorter session.
		quirev1.AuthService_RefreshSession_FullMethodName: true,
		// UC08, both halves.
		quirev1.AuthService_RequestPasswordRecovery_FullMethodName: true,
		quirev1.AuthService_ResetPassword_FullMethodName:           true,
	}

	got := authn.PublicMethods()
	if len(got) != len(want) {
		t.Fatalf("%d methods are public, want %d", len(got), len(want))
	}

	for _, method := range got {
		if !want[method] {
			t.Errorf("%s is public and should not be", method)
		}
	}
}

func TestUnaryRefusesACredentialItCannotRead(t *testing.T) {
	t.Parallel()

	f := newFixture()

	tests := []struct {
		name string
		md   metadata.MD
		code string
	}{
		{name: "no metadata at all", md: nil, code: authn.CodeNoToken},
		{name: "no authorization header", md: metadata.Pairs("x-request-id", "1"), code: authn.CodeNoToken},
		{
			name: "a scheme this node does not accept",
			md:   metadata.Pairs("authorization", "Basic abc"), code: authn.CodeMalformedToken,
		},
		{
			name: "nothing after the scheme",
			md:   metadata.Pairs("authorization", "Bearer   "), code: authn.CodeNoToken,
		},
		{name: "no scheme", md: metadata.Pairs("authorization", "abc"), code: authn.CodeMalformedToken},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := f.call(t, quirev1.AuthService_GetUser_FullMethodName, test.md)
			if err == nil {
				t.Fatal("the interceptor let the call through")
			}

			if code := errs.CodeOf(err); code != test.code {
				t.Errorf("code = %q, want %q", code, test.code)
			}
		})
	}
}

// TestUnaryAcceptsTheSchemeInAnyCase covers what clients actually send: the
// scheme is case-insensitive, and half of them lower-case it.
func TestUnaryAcceptsTheSchemeInAnyCase(t *testing.T) {
	t.Parallel()

	f := newFixture()

	token, _, err := f.auth.IssueAccess(uuid.New(), uuid.New(), now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		t.Run(scheme, func(t *testing.T) {
			t.Parallel()

			_, err := f.call(t, quirev1.AuthService_GetUser_FullMethodName,
				metadata.Pairs("authorization", scheme+" "+token))
			if err != nil {
				t.Errorf("the interceptor refused %q: %v", scheme, err)
			}
		})
	}
}

// TestUnaryRefusesATokenItDidNotIssue is the whole of what the interceptor
// checks: the signature, and nothing about the database.
func TestUnaryRefusesATokenItDidNotIssue(t *testing.T) {
	t.Parallel()

	f := newFixture()

	_, err := f.call(t, quirev1.AuthService_GetUser_FullMethodName, bearer("not a token"))
	if err == nil {
		t.Fatal("the interceptor accepted a token it did not issue")
	}

	if !errors.Is(err, errs.KindUnauthenticated) {
		t.Errorf("error = %v, want unauthenticated", err)
	}
}

// TestRequireOnAnUnauthenticatedContext is the fault of this node rather than of
// the caller: reaching a handler with no identity means the method was left out
// of the public set.
func TestRequireOnAnUnauthenticatedContext(t *testing.T) {
	t.Parallel()

	_, err := authn.Require(t.Context())
	if err == nil {
		t.Fatal("Require without an identity = nil, want an error")
	}

	if !errors.Is(err, errs.KindInternal) {
		t.Errorf("error = %v, want internal", err)
	}
}

// TestStreamStampsTheIdentity covers the chain the synchronization service is
// served through.
func TestStreamStampsTheIdentity(t *testing.T) {
	t.Parallel()

	f := newFixture()
	userID, deviceID := uuid.New(), uuid.New()

	token, _, err := f.auth.IssueAccess(userID, deviceID, now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	ctx := metadata.NewIncomingContext(t.Context(), bearer(token))

	var served context.Context

	err = f.interceptor.Stream()(nil, &serverStream{ctx: ctx},
		&grpc.StreamServerInfo{FullMethod: "/quire.v1.SyncService/Sync"},
		func(_ any, stream grpc.ServerStream) error {
			served = stream.Context()

			return nil
		})
	if err != nil {
		t.Fatalf("the interceptor refused a valid token on a stream: %v", err)
	}

	identity, ok := authn.From(served)
	if !ok {
		t.Fatal("the stream handler was reached without an identity")
	}

	if identity.UserID != userID || identity.DeviceID != deviceID {
		t.Error("the identity on the stream is not the one the token asserts")
	}
}

// serverStream is the least a grpc.ServerStream can be and still carry a
// context.
type serverStream struct {
	grpc.ServerStream

	ctx context.Context
}

func (s *serverStream) Context() context.Context { return s.ctx }
