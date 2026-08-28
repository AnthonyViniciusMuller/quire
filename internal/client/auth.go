package client

import (
	"context"
	"time"
	"uuid"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The paths an update to a reader or a device may name. They are the
// contract's, and they are the same names the node's controllers admit.
const (
	pathDisplayName = "display_name"
	pathEmail       = "email"
	pathName        = "name"
)

// Registration is a reader binding themselves to a node (UC14).
type Registration struct {
	LocalName   string
	DisplayName string
	Email       string
	Password    string
}

// Credentials is a reader proving who they are, together with the device the
// session is for (UC07).
//
// Either half of the identifier may be given and the client sends whichever is
// filled in, because the contract says which was meant rather than leaving the
// node to parse it.
type Credentials struct {
	LocalName string
	Email     string
	Password  string

	// DeviceName and DevicePlatform name this device to its reader. They are
	// used when the device is being bound for the first time; afterwards the
	// binding carries the identifier the state remembers and the node keeps
	// the name it already has.
	DeviceName     string
	DevicePlatform string
}

// Register creates the reader on this node, which is what binds them to it: the
// server half of the identifier is not a parameter, it is which node the call
// was addressed to.
func (c *Client) Register(ctx context.Context, in *Registration) (*quirev1.User, error) {
	if err := c.requireOnline("register"); err != nil {
		return nil, err
	}

	response, err := c.auth.RegisterUser(ctx, &quirev1.RegisterUserRequest{
		LocalName:   in.LocalName,
		DisplayName: in.DisplayName,
		Email:       in.Email,
		Password:    in.Password,
	})
	if err != nil {
		return nil, err
	}

	return response.GetUser(), nil
}

// Login verifies the password, binds this device if it is not bound yet, and
// stores the session it is issued.
//
// The device identifier the state remembers is what the binding carries, and
// that is the whole point of keeping it: a device that forgot it would be bound
// a second time, under a second identifier, and would start a clock entry that
// never merges with the one its own earlier writes are counted under.
func (c *Client) Login(ctx context.Context, in *Credentials) (*quirev1.LoginResponse, error) {
	if err := c.requireOnline("login"); err != nil {
		return nil, err
	}

	request := &quirev1.LoginRequest{
		Password: in.Password,
		Device: &quirev1.DeviceBinding{
			Name:     in.DeviceName,
			Platform: in.DevicePlatform,
		},
	}

	if in.Email != "" {
		request.LoginId = &quirev1.LoginRequest_Email{Email: in.Email}
	} else {
		request.LoginId = &quirev1.LoginRequest_LocalName{LocalName: in.LocalName}
	}

	if bound := c.state.Device.ID; bound != (uuid.UUID{}) {
		request.Device.DeviceId = proto(bound.String())
	}

	response, err := c.auth.Login(ctx, request)
	if err != nil {
		return nil, err
	}

	c.adopt(response.GetUser(), response.GetDevice(), response.GetSession())

	if err := c.save(); err != nil {
		return nil, err
	}

	return response, nil
}

// adopt records who this device now is.
//
// What it does not do is clear anything. A session for a reader whose
// identifier is not the one the state was holding is the ordinary shape of
// UC16 — a reader who moved keeps their devices and gets a new identifier on
// the new node (C11) — and the devices that did not make the call arrive at
// their new home by logging into it, holding a log this node has not seen. A
// client that emptied the causal state on the change of account would complete
// the migration by discarding what was being migrated.
//
// The state file is therefore one device of one reader, and pointing it at a
// second reader is an operator's mistake rather than a case handled here. It is
// not a dangerous one: what such a client offered would name records belonging
// to somebody else, and the node refuses those on the reader they belong to.
func (c *Client) adopt(reader *quirev1.User, appliance *quirev1.Device, session *quirev1.Session) {
	c.state.User = User{
		ID:          parseID(reader.GetId()),
		LocalName:   reader.GetLocalName(),
		FederatedID: reader.GetFederatedId(),
	}

	c.state.Server = Server{Address: c.Address(), Domain: reader.GetOriginServerDomain()}

	if appliance != nil {
		c.state.Device = Device{
			ID:       parseID(appliance.GetId()),
			Name:     appliance.GetName(),
			Platform: appliance.GetPlatform(),
		}
	}

	c.storeSession(session)
}

// storeSession records what the device presents from now on.
func (c *Client) storeSession(session *quirev1.Session) {
	if session == nil {
		return
	}

	c.state.Session = Session{
		AccessToken:           session.GetAccessToken(),
		AccessTokenExpiresAt:  session.GetAccessTokenExpiresAt().AsTime(),
		RefreshToken:          session.GetRefreshToken(),
		RefreshTokenExpiresAt: session.GetRefreshTokenExpiresAt().AsTime(),
	}
}

// Logout consumes the refresh credential, which ends this device's session and
// no other device's (RN07).
//
// The credential is presented rather than inferred from the session, so a
// device whose access token has already expired can still end its session
// without refreshing first — which is why this method does not go through
// [Client.authorized].
func (c *Client) Logout(ctx context.Context) error {
	if err := c.requireOnline("logout"); err != nil {
		return err
	}

	if c.state.Session.RefreshToken == "" {
		return errs.New(errs.KindUnauthenticated, "this device has no session").
			WithOp(opClient).
			WithCode(CodeNoSession).
			WithField("session", "there is nothing to end")
	}

	if _, err := c.auth.Logout(ctx, &quirev1.LogoutRequest{
		RefreshToken: c.state.Session.RefreshToken,
	}); err != nil {
		return err
	}

	// The device identity stays. It is what this device's writes are counted
	// under, and logging out is not ceasing to be the device that made them.
	c.state.Session = Session{}

	return c.save()
}

// Refresh exchanges the refresh credential for a new session.
//
// The credential is rotated rather than reused: the one presented is consumed
// and the reply carries its replacement, so the state has to be written before
// the next call is made. A client that lost the reply has lost the session,
// which is D07's trade and is the reason this writes the file immediately.
func (c *Client) Refresh(ctx context.Context) error {
	if err := c.requireOnline("refresh"); err != nil {
		return err
	}

	if c.state.Session.RefreshToken == "" {
		return errs.New(errs.KindUnauthenticated, "this device has no session").
			WithOp(opClient).
			WithCode(CodeNoSession).
			WithField("session", "log in first")
	}

	if expiry := c.state.Session.RefreshTokenExpiresAt; !expiry.IsZero() && time.Now().After(expiry) {
		return errs.New(errs.KindUnauthenticated, "this device's session has expired").
			WithOp(opClient).
			WithCode(CodeNoSession).
			WithField("session", "log in again")
	}

	response, err := c.auth.RefreshSession(ctx, &quirev1.RefreshSessionRequest{
		RefreshToken: c.state.Session.RefreshToken,
	})
	if err != nil {
		return err
	}

	c.storeSession(response.GetSession())

	return c.save()
}

// Whoami returns the reader's own record, which is the only record of a reader
// this node serves to anybody (RN09).
func (c *Client) Whoami(ctx context.Context) (*quirev1.User, error) {
	authorized, err := c.call(ctx, "whoami")
	if err != nil {
		return nil, err
	}

	response, err := c.auth.GetUser(authorized, &quirev1.GetUserRequest{})
	if err != nil {
		return nil, err
	}

	return response.GetUser(), nil
}

// UpdateUser writes the two fields of a reader that are writable. A nil pointer
// is a field the call does not claim, which is not the same as one it clears.
func (c *Client) UpdateUser(ctx context.Context, displayName, email *string) (*quirev1.User, error) {
	authorized, err := c.call(ctx, "update the reader")
	if err != nil {
		return nil, err
	}

	reader := &quirev1.User{}
	mask := &fieldmaskpb.FieldMask{}

	if displayName != nil {
		reader.DisplayName = *displayName

		mask.Paths = append(mask.Paths, pathDisplayName)
	}

	if email != nil {
		reader.Email = email

		mask.Paths = append(mask.Paths, pathEmail)
	}

	response, err := c.auth.UpdateUser(authorized, &quirev1.UpdateUserRequest{
		User:       reader,
		UpdateMask: mask,
	})
	if err != nil {
		return nil, err
	}

	return response.GetUser(), nil
}

// ChangePassword takes the old password as well as the new one, which is what a
// field mask cannot express and why the contract gives it a call of its own.
func (c *Client) ChangePassword(ctx context.Context, current, next string) error {
	authorized, err := c.call(ctx, "change the password")
	if err != nil {
		return err
	}

	if _, err = c.auth.ChangePassword(authorized, &quirev1.ChangePasswordRequest{
		CurrentPassword: current,
		NewPassword:     next,
	}); err != nil {
		return err
	}

	// Every session ends with the change, this one included, so the credential
	// the state holds is spent and keeping it would only produce a refusal at
	// the next call.
	c.state.Session = Session{}

	return c.save()
}

// DeleteUser removes the reader from this node with everything that cascades
// from them. It is not a migration: UC16 is [Client.Migrate].
func (c *Client) DeleteUser(ctx context.Context, password string) error {
	authorized, err := c.call(ctx, "delete the reader")
	if err != nil {
		return err
	}

	if _, err = c.auth.DeleteUser(authorized, &quirev1.DeleteUserRequest{Password: password}); err != nil {
		return err
	}

	c.state.Session = Session{}
	c.state.User = User{}

	return c.save()
}

// RegisterDevice binds a device without logging in with it, which is how a
// reader pairs a second application from an already authenticated one (RF11).
func (c *Client) RegisterDevice(ctx context.Context, name, platform string) (*quirev1.Device, error) {
	authorized, err := c.call(ctx, "register a device")
	if err != nil {
		return nil, err
	}

	response, err := c.auth.RegisterDevice(authorized, &quirev1.RegisterDeviceRequest{
		Name:     name,
		Platform: platform,
	})
	if err != nil {
		return nil, err
	}

	return response.GetDevice(), nil
}

// ListDevices returns the reader's devices, which is what gives a vector clock
// entry a name.
func (c *Client) ListDevices(ctx context.Context, includeInactive bool) ([]*quirev1.Device, error) {
	authorized, err := c.call(ctx, "list the devices")
	if err != nil {
		return nil, err
	}

	response, err := c.auth.ListDevices(authorized, &quirev1.ListDevicesRequest{
		IncludeInactive: includeInactive,
	})
	if err != nil {
		return nil, err
	}

	return response.GetDevices(), nil
}

// RenameDevice renames one. Nothing else about a bound device is editable: its
// platform is what it is, and its identifier is referenced by every clock it
// appears in.
func (c *Client) RenameDevice(ctx context.Context, device uuid.UUID, name string) (*quirev1.Device, error) {
	authorized, err := c.call(ctx, "rename a device")
	if err != nil {
		return nil, err
	}

	response, err := c.auth.UpdateDevice(authorized, &quirev1.UpdateDeviceRequest{
		DeviceId:   device.String(),
		Device:     &quirev1.Device{Name: name},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{pathName}},
	})
	if err != nil {
		return nil, err
	}

	if device == c.state.Device.ID {
		c.state.Device.Name = response.GetDevice().GetName()

		if err := c.save(); err != nil {
			return nil, err
		}
	}

	return response.GetDevice(), nil
}

// RevokeDevice unbinds a device: it may no longer write, and its sessions end.
// The record survives, because every operation it ever authored is still keyed
// by its identifier.
func (c *Client) RevokeDevice(ctx context.Context, device uuid.UUID) error {
	authorized, err := c.call(ctx, "revoke a device")
	if err != nil {
		return err
	}

	_, err = c.auth.RevokeDevice(authorized, &quirev1.RevokeDeviceRequest{DeviceId: device.String()})

	return err
}

// call is the context an authenticated call is made on: the client must be
// connected, and the session must be presentable.
func (c *Client) call(ctx context.Context, what string) (context.Context, error) {
	if err := c.requireOnline(what); err != nil {
		return nil, err
	}

	return c.authorized(ctx)
}

// proto returns a pointer to value, for the optional fields of the contract.
func proto[T any](value T) *T { return &value }
