package authservice_test

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	changepasswordusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/changepassword"
	deleteuserusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/deleteuser"
	getuserusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/getuser"
	listdevicesusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/listdevices"
	loginusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/login"
	logoutusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/logout"
	refreshusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/refresh"
	registerusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	registerdeviceusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/registerdevice"
	requestrecoveryusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/requestrecovery"
	resetpasswordusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/resetpassword"
	revokedeviceusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/revokedevice"
	updatedeviceusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/updatedevice"
	updateuserusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/updateuser"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authservice"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/changepassword"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/deleteuser"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/getuser"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/listdevices"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/login"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/logout"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/refreshsession"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/registerdevice"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/registeruser"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/requestpasswordrecovery"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/resetpassword"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/revokedevice"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/updatedevice"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/updateuser"
)

// errReached is what a recorder reports instead of doing the work: the call got
// this far, and the test is about how far that is.
var errReached = errors.New("the use case was reached")

// recorder stands in for a use case and writes down that it ran.
type recorder[In, Out any] struct {
	name  string
	calls *[]string
}

func (r recorder[In, Out]) Execute(_ context.Context, _ In) (Out, error) {
	var zero Out

	*r.calls = append(*r.calls, r.name)

	return zero, errReached
}

// TestEveryCallReachesItsController is what the embedded Unimplemented struct
// costs, paid back.
//
// buf.gen.yaml keeps that embedding on purpose, so a method left out of this
// service compiles and answers Unimplemented rather than failing to build. This
// calls all fourteen and refuses that answer — and, because each stand-in has a
// name, it also refuses a forwarding method wired to the wrong controller,
// which is the mistake a file of fourteen near-identical methods invites.
func TestEveryCallReachesItsController(t *testing.T) {
	t.Parallel()

	var calls []string

	service := authservice.New(&authservice.Controllers{
		RegisterUser: registeruser.New(
			recorder[registerusecase.Input, registerusecase.Output]{name: "RegisterUser", calls: &calls}),
		GetUser: getuser.New(
			recorder[getuserusecase.Input, getuserusecase.Output]{name: "GetUser", calls: &calls}),
		UpdateUser: updateuser.New(
			recorder[updateuserusecase.Input, updateuserusecase.Output]{name: "UpdateUser", calls: &calls}),
		ChangePassword: changepassword.New(
			recorder[changepasswordusecase.Input, changepasswordusecase.Output]{
				name: "ChangePassword", calls: &calls,
			}),
		DeleteUser: deleteuser.New(
			recorder[deleteuserusecase.Input, deleteuserusecase.Output]{name: "DeleteUser", calls: &calls}),
		Login: login.New(
			recorder[loginusecase.Input, loginusecase.Output]{name: "Login", calls: &calls}),
		Logout: logout.New(
			recorder[logoutusecase.Input, logoutusecase.Output]{name: "Logout", calls: &calls}),
		RefreshSession: refreshsession.New(
			recorder[refreshusecase.Input, refreshusecase.Output]{name: "RefreshSession", calls: &calls}),
		RequestPasswordRecovery: requestpasswordrecovery.New(
			recorder[requestrecoveryusecase.Input, requestrecoveryusecase.Output]{
				name: "RequestPasswordRecovery", calls: &calls,
			}),
		ResetPassword: resetpassword.New(
			recorder[resetpasswordusecase.Input, resetpasswordusecase.Output]{name: "ResetPassword", calls: &calls}),
		RegisterDevice: registerdevice.New(
			recorder[registerdeviceusecase.Input, registerdeviceusecase.Output]{
				name: "RegisterDevice", calls: &calls,
			}),
		ListDevices: listdevices.New(
			recorder[listdevicesusecase.Input, listdevicesusecase.Output]{name: "ListDevices", calls: &calls}),
		UpdateDevice: updatedevice.New(
			recorder[updatedeviceusecase.Input, updatedeviceusecase.Output]{name: "UpdateDevice", calls: &calls}),
		RevokeDevice: revokedevice.New(
			recorder[revokedeviceusecase.Input, revokedeviceusecase.Output]{name: "RevokeDevice", calls: &calls}),
	})

	ctx := authenticated(t)
	deviceID := uuid.New().String()

	tests := []struct {
		name string
		call func() error
	}{
		{"RegisterUser", func() error {
			_, err := service.RegisterUser(ctx, &quirev1.RegisterUserRequest{})

			return err
		}},
		{"GetUser", func() error {
			_, err := service.GetUser(ctx, &quirev1.GetUserRequest{})

			return err
		}},
		{"UpdateUser", func() error {
			_, err := service.UpdateUser(ctx, &quirev1.UpdateUserRequest{})

			return err
		}},
		{"ChangePassword", func() error {
			_, err := service.ChangePassword(ctx, &quirev1.ChangePasswordRequest{})

			return err
		}},
		{"DeleteUser", func() error {
			_, err := service.DeleteUser(ctx, &quirev1.DeleteUserRequest{})

			return err
		}},
		{"Login", func() error {
			_, err := service.Login(ctx, &quirev1.LoginRequest{
				LoginId: &quirev1.LoginRequest_LocalName{LocalName: "anthony"},
			})

			return err
		}},
		{"Logout", func() error {
			_, err := service.Logout(ctx, &quirev1.LogoutRequest{})

			return err
		}},
		{"RefreshSession", func() error {
			_, err := service.RefreshSession(ctx, &quirev1.RefreshSessionRequest{})

			return err
		}},
		{"RequestPasswordRecovery", func() error {
			_, err := service.RequestPasswordRecovery(ctx, &quirev1.RequestPasswordRecoveryRequest{})

			return err
		}},
		{"ResetPassword", func() error {
			_, err := service.ResetPassword(ctx, &quirev1.ResetPasswordRequest{})

			return err
		}},
		{"RegisterDevice", func() error {
			_, err := service.RegisterDevice(ctx, &quirev1.RegisterDeviceRequest{})

			return err
		}},
		{"ListDevices", func() error {
			_, err := service.ListDevices(ctx, &quirev1.ListDevicesRequest{})

			return err
		}},
		{"UpdateDevice", func() error {
			_, err := service.UpdateDevice(ctx, &quirev1.UpdateDeviceRequest{DeviceId: deviceID})

			return err
		}},
		{"RevokeDevice", func() error {
			_, err := service.RevokeDevice(ctx, &quirev1.RevokeDeviceRequest{DeviceId: deviceID})

			return err
		}},
	}

	for _, test := range tests {
		calls = calls[:0]

		err := test.call()

		if status.Code(err) == codes.Unimplemented {
			t.Errorf("%s answers Unimplemented, so the service does not serve it", test.name)

			continue
		}

		if !errors.Is(err, errReached) {
			t.Errorf("%s did not reach a use case: %v", test.name, err)

			continue
		}

		if len(calls) != 1 || calls[0] != test.name {
			t.Errorf("%s reached %v, want its own controller", test.name, calls)
		}
	}
}

// authenticated is a context carrying an identity, built by running the real
// interceptor rather than by reaching into it.
func authenticated(t *testing.T) context.Context {
	t.Helper()

	auth := apptest.NewAuthService()
	clock := apptest.NewClock(time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC))

	token, _, err := auth.IssueAccess(uuid.New(), uuid.New(), clock.Now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	incoming := metadata.NewIncomingContext(t.Context(), metadata.Pairs("authorization", "Bearer "+token))

	var served context.Context

	_, err = authn.New(auth, clock, nil).Unary()(incoming, nil,
		&grpc.UnaryServerInfo{FullMethod: quirev1.AuthService_GetUser_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			served = ctx

			return nil, nil //nolint:nilnil // the handler only captures the context.
		})
	if err != nil {
		t.Fatalf("building an authenticated context: %v", err)
	}

	return served
}
