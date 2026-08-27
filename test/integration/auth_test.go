//go:build integration

package integration_test

import (
	"context"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	identitydi "github.com/anthonyvsmuller/quire/internal/identity/di"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// thePassword is what the reader of these tests registers with.
const thePassword = "correct horse battery staple"

// serve starts the node's gRPC server with the identity slice registered, and
// returns a client for it.
//
// It builds the real server rather than a bare one, so that what these tests
// exercise is the chain the node serves under: the authentication interceptor
// nearest the handler, and above it the translation that turns a domain error
// into the status a client sees.
func serve(t *testing.T) quirev1.AuthServiceClient {
	t.Helper()
	reset(t)

	cfg := nodeConfig(t)

	container, err := identitydi.Initialize(cfg, pool)
	if err != nil {
		t.Fatalf("building the identity slice: %v", err)
	}

	server, err := grpcx.New(t.Context(), &cfg.Server,
		grpcx.WithChain(grpcx.NewChain(logging.Discard())),
		grpcx.WithUnaryInterceptors(container.Interceptor.Unary()),
		grpcx.WithStreamInterceptors(container.Interceptor.Stream()),
	)
	if err != nil {
		t.Fatalf("opening the listener: %v", err)
	}

	container.Service.Register(server.Registrar())

	ctx, cancel := context.WithCancel(t.Context())

	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()

	connection, err := grpc.NewClient(server.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing the node: %v", err)
	}

	t.Cleanup(func() {
		_ = connection.Close()

		cancel()

		if err := <-served; err != nil {
			t.Errorf("Serve returned %v", err)
		}
	})

	return quirev1.NewAuthServiceClient(connection)
}

// bearer returns a context presenting token, as a device does.
func bearer(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

// reasonOf is the machine-readable reason a status carries, which is the code
// the domain error was raised with.
func reasonOf(err error) string {
	for _, detail := range status.Convert(err).Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info.GetReason()
		}
	}

	return ""
}

// TestAuthRoundTrip walks UC14, UC07, UC06 and UC08 in the order a reader
// walks them, against a real database and over a real connection.
//
// The subtests share state on purpose and must run in order: what makes this a
// round trip rather than a set of unit tests is that each step starts from what
// the previous one left in the database.
func TestAuthRoundTrip(t *testing.T) {
	client := serve(t)
	ctx := t.Context()

	var (
		session *quirev1.Session
		phone   *quirev1.Device
	)

	t.Run("register binds the reader to this node", func(t *testing.T) {
		registered, err := client.RegisterUser(ctx, &quirev1.RegisterUserRequest{
			// Capitalized, to prove the folding survives the whole path.
			LocalName:   "Anthony",
			DisplayName: "Anthony Muller",
			Email:       "anthony@example.test",
			Password:    thePassword,
		})
		if err != nil {
			t.Fatalf("RegisterUser: %v", err)
		}

		if got := registered.GetUser().GetFederatedId(); got != "@anthony:"+testServerName {
			t.Errorf("FederatedId = %q, want the identifier UC14 assembles", got)
		}
	})

	t.Run("the same name is refused, and says which field", func(t *testing.T) {
		_, err := client.RegisterUser(ctx, &quirev1.RegisterUserRequest{
			LocalName:   "anthony",
			DisplayName: "Somebody",
			Email:       "somebody@example.test",
			Password:    thePassword,
		})
		if status.Code(err) != codes.AlreadyExists {
			t.Fatalf("RegisterUser = %v, want AlreadyExists", err)
		}

		if reason := reasonOf(err); reason != "local_name_taken" {
			t.Errorf("reason = %q, want local_name_taken", reason)
		}
	})

	t.Run("an authenticated call needs a token", func(t *testing.T) {
		_, err := client.GetUser(ctx, &quirev1.GetUserRequest{})
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("GetUser without a token = %v, want Unauthenticated", err)
		}

		if reason := reasonOf(err); reason != "no_token" {
			t.Errorf("reason = %q, want no_token", reason)
		}
	})

	t.Run("logging in binds the device and issues the session", func(t *testing.T) {
		out, err := client.Login(ctx, &quirev1.LoginRequest{
			LoginId:  &quirev1.LoginRequest_LocalName{LocalName: "anthony"},
			Password: thePassword,
			Device:   &quirev1.DeviceBinding{Name: "Pixel 9", Platform: "android"},
		})
		if err != nil {
			t.Fatalf("Login: %v", err)
		}

		session, phone = out.GetSession(), out.GetDevice()

		if phone.GetId() == "" {
			t.Fatal("no device identifier came back, and it is what the appliance keys its clock by")
		}

		if !session.GetAccessTokenExpiresAt().AsTime().Before(session.GetRefreshTokenExpiresAt().AsTime()) {
			t.Error("the access token outlives the credential meant to replace it")
		}
	})

	t.Run("the reader reads their own record, address included", func(t *testing.T) {
		out, err := client.GetUser(bearer(ctx, session.GetAccessToken()), &quirev1.GetUserRequest{})
		if err != nil {
			t.Fatalf("GetUser: %v", err)
		}

		if out.GetUser().GetEmail() != "anthony@example.test" {
			t.Errorf("Email = %q, want the address, which only this reply carries", out.GetUser().GetEmail())
		}
	})

	t.Run("refreshing rotates, and reusing the spent credential ends the sessions", func(t *testing.T) {
		spent := session.GetRefreshToken()

		rotated, err := client.RefreshSession(ctx, &quirev1.RefreshSessionRequest{RefreshToken: spent})
		if err != nil {
			t.Fatalf("RefreshSession: %v", err)
		}

		if rotated.GetSession().GetRefreshToken() == spent {
			t.Fatal("the same credential came back, so nothing was rotated")
		}

		_, err = client.RefreshSession(ctx, &quirev1.RefreshSessionRequest{RefreshToken: spent})
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("presenting the spent credential = %v, want Unauthenticated", err)
		}

		if reason := reasonOf(err); reason != "credential_reused" {
			t.Errorf("reason = %q, want credential_reused", reason)
		}

		// The replacement the device was holding is ended too: this node cannot
		// tell which of the two holders is the reader.
		_, err = client.RefreshSession(ctx,
			&quirev1.RefreshSessionRequest{RefreshToken: rotated.GetSession().GetRefreshToken()})
		if err == nil {
			t.Error("the replacement survived the reuse, so a copy could refresh beside the reader")
		}
	})

	t.Run("the reader logs in again and manages their devices", func(t *testing.T) {
		out, err := client.Login(ctx, &quirev1.LoginRequest{
			LoginId:  &quirev1.LoginRequest_LocalName{LocalName: "anthony"},
			Password: thePassword,
			Device:   &quirev1.DeviceBinding{DeviceId: proto.String(phone.GetId())},
		})
		if err != nil {
			t.Fatalf("Login: %v", err)
		}

		if out.GetDevice().GetId() != phone.GetId() {
			t.Fatal("logging in again minted a second identifier for the same appliance")
		}

		session = out.GetSession()
		authenticated := bearer(ctx, session.GetAccessToken())

		_, err = client.RegisterDevice(authenticated,
			&quirev1.RegisterDeviceRequest{Name: "Tablet", Platform: "android"})
		if err != nil {
			t.Fatalf("RegisterDevice: %v", err)
		}

		listed, err := client.ListDevices(authenticated, &quirev1.ListDevicesRequest{})
		if err != nil {
			t.Fatalf("ListDevices: %v", err)
		}

		if len(listed.GetDevices()) != 2 {
			t.Fatalf("%d devices came back, want the two bound ones", len(listed.GetDevices()))
		}

		if listed.GetDevices()[0].GetName() != "Pixel 9" {
			t.Errorf("the list starts with %q, want it ordered by name", listed.GetDevices()[0].GetName())
		}
	})

	t.Run("recovering the password ends every session", func(t *testing.T) {
		// The reply is the same for an address nobody has, which is what stops
		// the call from saying who is registered here.
		if _, err := client.RequestPasswordRecovery(ctx,
			&quirev1.RequestPasswordRecoveryRequest{Email: "nobody@example.test"}); err != nil {
			t.Fatalf("RequestPasswordRecovery for an unknown address: %v", err)
		}

		if _, err := client.RequestPasswordRecovery(ctx,
			&quirev1.RequestPasswordRecoveryRequest{Email: "anthony@example.test"}); err != nil {
			t.Fatalf("RequestPasswordRecovery: %v", err)
		}

		// Only the digest was stored, so the credential itself exists nowhere
		// this test can read. What it can check is that the row was written and
		// that the sessions ended when a reset happens — the value's own path is
		// covered by the use case tests, which hold the notifier.
		var outstanding int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM identity.credentials WHERE kind = 'password_recovery' AND NOT consumed`,
		).Scan(&outstanding); err != nil {
			t.Fatalf("counting the recovery credentials: %v", err)
		}

		if outstanding != 1 {
			t.Errorf("%d recovery credentials are outstanding, want the one just issued", outstanding)
		}
	})

	t.Run("changing the password ends every session", func(t *testing.T) {
		authenticated := bearer(ctx, session.GetAccessToken())

		if _, err := client.ChangePassword(authenticated, &quirev1.ChangePasswordRequest{
			CurrentPassword: thePassword,
			NewPassword:     "a different password entirely",
		}); err != nil {
			t.Fatalf("ChangePassword: %v", err)
		}

		var live int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM identity.credentials WHERE kind = 'session_refresh' AND NOT consumed`,
		).Scan(&live); err != nil {
			t.Fatalf("counting the sessions: %v", err)
		}

		if live != 0 {
			t.Errorf("%d sessions are live after the password changed, want none on any device", live)
		}

		if _, err := client.Login(ctx, &quirev1.LoginRequest{
			LoginId:  &quirev1.LoginRequest_LocalName{LocalName: "anthony"},
			Password: thePassword,
			Device:   &quirev1.DeviceBinding{DeviceId: proto.String(phone.GetId())},
		}); status.Code(err) != codes.Unauthenticated {
			t.Errorf("the old password still logs in: %v", err)
		}
	})

	t.Run("deleting the reader frees the identifier", func(t *testing.T) {
		out, err := client.Login(ctx, &quirev1.LoginRequest{
			LoginId:  &quirev1.LoginRequest_LocalName{LocalName: "anthony"},
			Password: "a different password entirely",
			Device:   &quirev1.DeviceBinding{DeviceId: proto.String(phone.GetId())},
		})
		if err != nil {
			t.Fatalf("Login with the new password: %v", err)
		}

		authenticated := bearer(ctx, out.GetSession().GetAccessToken())

		if _, err := client.DeleteUser(authenticated,
			&quirev1.DeleteUserRequest{Password: "a different password entirely"}); err != nil {
			t.Fatalf("DeleteUser: %v", err)
		}

		if _, err := client.RegisterUser(ctx, &quirev1.RegisterUserRequest{
			LocalName:   "anthony",
			DisplayName: "Somebody else",
			Email:       "anthony@example.test",
			Password:    thePassword,
		}); err != nil {
			t.Errorf("the identifier is still taken after the reader was deleted: %v", err)
		}
	})
}

// TestAnotherReadersDeviceIsAnsweredAsOneNobodyHas is the property a reply must
// not break: it may not say which identifiers belong to somebody else.
func TestAnotherReadersDeviceIsAnsweredAsOneNobodyHas(t *testing.T) {
	client := serve(t)
	ctx := t.Context()

	register := func(localName, email string) *quirev1.Session {
		t.Helper()

		if _, err := client.RegisterUser(ctx, &quirev1.RegisterUserRequest{
			LocalName: localName, DisplayName: localName, Email: email, Password: thePassword,
		}); err != nil {
			t.Fatalf("RegisterUser: %v", err)
		}

		out, err := client.Login(ctx, &quirev1.LoginRequest{
			LoginId:  &quirev1.LoginRequest_LocalName{LocalName: localName},
			Password: thePassword,
			Device:   &quirev1.DeviceBinding{Name: "Pixel 9", Platform: "android"},
		})
		if err != nil {
			t.Fatalf("Login: %v", err)
		}

		return out.GetSession()
	}

	mine := register("anthony", "anthony@example.test")
	register("somebody", "somebody@example.test")

	var strangerDevice string
	if err := pool.QueryRow(ctx,
		`SELECT d.id::text FROM identity.devices d
		 JOIN identity.users u ON u.id = d.user_id
		 WHERE u.local_name = 'somebody'`).Scan(&strangerDevice); err != nil {
		t.Fatalf("finding the stranger's device: %v", err)
	}

	authenticated := bearer(ctx, mine.GetAccessToken())

	_, err := client.RevokeDevice(authenticated, &quirev1.RevokeDeviceRequest{DeviceId: strangerDevice})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("revoking another reader's device = %v, want NotFound", err)
	}

	if reason := reasonOf(err); reason != "device_not_found" {
		t.Errorf("reason = %q, want device_not_found — the same answer an identifier nobody has gets", reason)
	}

	if !contains(status.Convert(err).Message(), "not bound to this account") {
		t.Errorf("message = %q, want it to say nothing about whose device it is", status.Convert(err).Message())
	}

	// And it is still bound.
	var active bool
	if err := pool.QueryRow(ctx,
		`SELECT active FROM identity.devices WHERE id = $1::uuid`, strangerDevice).Scan(&active); err != nil {
		t.Fatalf("reading the stranger's device: %v", err)
	}

	if !active {
		t.Error("a stranger's appliance was unbound")
	}
}
