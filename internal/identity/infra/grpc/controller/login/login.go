// Package login serves AuthService.Login (UC07, first half).
package login

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/login"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opHandle is the operation reported by this file, in the form the errs package
// expects.
const opHandle = "identity/login: handle"

// CodeNoReaderNamed is a request whose oneof carries neither half.
const CodeNoReaderNamed = "no_reader_named"

// Login serves the call.
type Login struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(login command.Usecase[usecase.Input, usecase.Output]) *Login {
	return &Login{usecase: login}
}

// Handle authenticates the reader and issues the session.
//
// The oneof is read here rather than passed through, because the wire type and
// the use case's input are different shapes: the contract makes the choice
// explicit so that a server never has to guess which half was meant by looking
// for an at sign, and the use case takes both fields with only one of them set.
func (c *Login) Handle(ctx context.Context, request *quirev1.LoginRequest) (*quirev1.LoginResponse, error) {
	input := usecase.Input{
		Password: request.GetPassword(),
		Device: usecase.Binding{
			DeviceID: request.GetDevice().GetDeviceId(),
			Name:     request.GetDevice().GetName(),
			Platform: request.GetDevice().GetPlatform(),
		},
	}

	switch id := request.GetLoginId().(type) {
	case *quirev1.LoginRequest_LocalName:
		input.LocalName = id.LocalName
	case *quirev1.LoginRequest_Email:
		input.Email = id.Email
	default:
		return nil, errs.New(errs.KindInvalidArgument, "the request names no reader").
			WithOp(opHandle).
			WithCode(CodeNoReaderNamed).
			WithField("login_id", "it must carry either a local name or an e-mail address")
	}

	output, err := c.usecase.Execute(ctx, input)
	if err != nil {
		return nil, err
	}

	return &quirev1.LoginResponse{
		Session: convert.Session(&output.Session),
		User:    convert.OwnUser(output.User, output.FederatedID),
		Device:  convert.Device(output.Device),
	}, nil
}
