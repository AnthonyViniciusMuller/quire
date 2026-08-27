// Package authservice registers the identity slice's gRPC service and hands
// each call to the controller that serves it.
//
// It is the whole of what the reference architecture calls the routes file. A
// gRPC service has no routing table — the generated interface is the table — so
// what remains is one forwarding method per call, and the value of keeping them
// here is that the list of what the slice serves is one file long.
//
// The Unimplemented struct is embedded because the contract requires it and
// because buf.gen.yaml says why: this contract grows an RPC in almost every
// phase, and a handler that did not compile until it implemented one would make
// every such phase start with unrelated work. What that costs is that a
// forgotten method answers Unimplemented instead of failing to build, so a test
// calls all fourteen and refuses that answer.
package authservice

import (
	"context"

	"google.golang.org/grpc"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
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

// Controllers is every handler the service forwards to, which the slice's
// container fills.
type Controllers struct {
	// RegisterUser serves UC14.
	RegisterUser *registeruser.RegisterUser
	// GetUser, UpdateUser, ChangePassword and DeleteUser serve UC06.
	GetUser        *getuser.GetUser
	UpdateUser     *updateuser.UpdateUser
	ChangePassword *changepassword.ChangePassword
	DeleteUser     *deleteuser.DeleteUser
	// Login, Logout and RefreshSession serve UC07 and what keeps it alive.
	Login          *login.Login
	Logout         *logout.Logout
	RefreshSession *refreshsession.RefreshSession
	// RequestPasswordRecovery and ResetPassword serve UC08.
	RequestPasswordRecovery *requestpasswordrecovery.RequestPasswordRecovery
	ResetPassword           *resetpassword.ResetPassword
	// RegisterDevice, ListDevices, UpdateDevice and RevokeDevice serve RF11.
	RegisterDevice *registerdevice.RegisterDevice
	ListDevices    *listdevices.ListDevices
	UpdateDevice   *updatedevice.UpdateDevice
	RevokeDevice   *revokedevice.RevokeDevice
}

// Service is the gRPC surface of the identity slice.
type Service struct {
	quirev1.UnimplementedAuthServiceServer

	controllers Controllers
}

// Service implements the generated server interface.
var _ quirev1.AuthServiceServer = (*Service)(nil)

// New returns the service over its controllers.
func New(controllers *Controllers) *Service {
	return &Service{controllers: *controllers}
}

// Register publishes the service on the node's gRPC server.
func (s *Service) Register(registrar grpc.ServiceRegistrar) {
	quirev1.RegisterAuthServiceServer(registrar, s)
}

// RegisterUser creates the reader on this node and binds them to it (UC14).
func (s *Service) RegisterUser(
	ctx context.Context, request *quirev1.RegisterUserRequest,
) (*quirev1.RegisterUserResponse, error) {
	return s.controllers.RegisterUser.Handle(ctx, request)
}

// GetUser answers the caller with their own record (UC06).
func (s *Service) GetUser(
	ctx context.Context, request *quirev1.GetUserRequest,
) (*quirev1.GetUserResponse, error) {
	return s.controllers.GetUser.Handle(ctx, request)
}

// UpdateUser changes the caller's record (UC06).
func (s *Service) UpdateUser(
	ctx context.Context, request *quirev1.UpdateUserRequest,
) (*quirev1.UpdateUserResponse, error) {
	return s.controllers.UpdateUser.Handle(ctx, request)
}

// ChangePassword replaces the caller's password (UC06).
func (s *Service) ChangePassword(
	ctx context.Context, request *quirev1.ChangePasswordRequest,
) (*quirev1.ChangePasswordResponse, error) {
	return s.controllers.ChangePassword.Handle(ctx, request)
}

// DeleteUser removes the caller from this node (UC06).
func (s *Service) DeleteUser(
	ctx context.Context, request *quirev1.DeleteUserRequest,
) (*quirev1.DeleteUserResponse, error) {
	return s.controllers.DeleteUser.Handle(ctx, request)
}

// Login verifies the password and issues the session (UC07).
func (s *Service) Login(
	ctx context.Context, request *quirev1.LoginRequest,
) (*quirev1.LoginResponse, error) {
	return s.controllers.Login.Handle(ctx, request)
}

// Logout ends the session of the device that presents the credential (UC07).
func (s *Service) Logout(
	ctx context.Context, request *quirev1.LogoutRequest,
) (*quirev1.LogoutResponse, error) {
	return s.controllers.Logout.Handle(ctx, request)
}

// RefreshSession exchanges a refresh credential for a new session.
func (s *Service) RefreshSession(
	ctx context.Context, request *quirev1.RefreshSessionRequest,
) (*quirev1.RefreshSessionResponse, error) {
	return s.controllers.RefreshSession.Handle(ctx, request)
}

// RequestPasswordRecovery sends a recovery credential to the address on record
// (UC08).
func (s *Service) RequestPasswordRecovery(
	ctx context.Context, request *quirev1.RequestPasswordRecoveryRequest,
) (*quirev1.RequestPasswordRecoveryResponse, error) {
	return s.controllers.RequestPasswordRecovery.Handle(ctx, request)
}

// ResetPassword consumes the recovery credential and sets the password (UC08).
func (s *Service) ResetPassword(
	ctx context.Context, request *quirev1.ResetPasswordRequest,
) (*quirev1.ResetPasswordResponse, error) {
	return s.controllers.ResetPassword.Handle(ctx, request)
}

// RegisterDevice binds an appliance without logging in with it (RF11).
func (s *Service) RegisterDevice(
	ctx context.Context, request *quirev1.RegisterDeviceRequest,
) (*quirev1.RegisterDeviceResponse, error) {
	return s.controllers.RegisterDevice.Handle(ctx, request)
}

// ListDevices answers with the caller's devices (RF11).
func (s *Service) ListDevices(
	ctx context.Context, request *quirev1.ListDevicesRequest,
) (*quirev1.ListDevicesResponse, error) {
	return s.controllers.ListDevices.Handle(ctx, request)
}

// UpdateDevice renames one of the caller's appliances.
func (s *Service) UpdateDevice(
	ctx context.Context, request *quirev1.UpdateDeviceRequest,
) (*quirev1.UpdateDeviceResponse, error) {
	return s.controllers.UpdateDevice.Handle(ctx, request)
}

// RevokeDevice unbinds one of the caller's appliances and ends its sessions.
func (s *Service) RevokeDevice(
	ctx context.Context, request *quirev1.RevokeDeviceRequest,
) (*quirev1.RevokeDeviceResponse, error) {
	return s.controllers.RevokeDevice.Handle(ctx, request)
}
