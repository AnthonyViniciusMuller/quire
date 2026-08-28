// Package updateuser serves AuthService.UpdateUser (UC06, update).
package updateuser

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/updateuser"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opHandle is the operation reported by this file, in the form the errs package
// expects.
const opHandle = "identity/updateuser: handle"

// CodeUnwritableField is a mask naming a field this call does not write.
const CodeUnwritableField = "unwritable_field"

// The paths the mask may carry. The address used to be the second of them and
// is now ChangeEmail's, per C14 in docs/tcc-corrections.md: it is the channel
// UC08 recovers an account through, so changing it takes the current password,
// and a field mask has no way to say that one of its paths needs a credential.
const (
	pathDisplayName = "display_name"
	pathEmail       = "email"
)

// UpdateUser serves the call.
type UpdateUser struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(update command.Usecase[usecase.Input, usecase.Output]) *UpdateUser {
	return &UpdateUser{usecase: update}
}

// Handle applies the fields the mask names.
//
// The mask is what turns the wire message into the use case's pointers: a
// client that is not touching a field and one that means to clear it look
// identical in a protobuf, and the mask is what tells them apart. A path this
// call does not write is refused rather than ignored — a client that asked to
// change the local name should be told that it cannot, not answered with a
// record where it did not change.
func (c *UpdateUser) Handle(
	ctx context.Context,
	request *quirev1.UpdateUserRequest,
) (*quirev1.UpdateUserResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	input := usecase.Input{UserID: identity.UserID}

	for _, path := range request.GetUpdateMask().GetPaths() {
		switch path {
		case pathDisplayName:
			displayName := request.GetUser().GetDisplayName()
			input.DisplayName = &displayName
		case pathEmail:
			// Named rather than folded into the default, because a client that
			// asked for it is asking to go around a password check and should
			// be told where the check is — not merely that the path is unknown.
			return nil, errs.New(errs.KindInvalidArgument,
				"the address is changed through ChangeEmail, which takes the current password").
				WithOp(opHandle).
				WithCode(CodeUnwritableField).
				WithField("update_mask", "it may not carry email")
		default:
			return nil, errs.Newf(errs.KindInvalidArgument, "%q is not a field this call writes", path).
				WithOp(opHandle).
				WithCode(CodeUnwritableField).
				WithField("update_mask", "only display_name may be changed")
		}
	}

	output, err := c.usecase.Execute(ctx, input)
	if err != nil {
		return nil, err
	}

	return &quirev1.UpdateUserResponse{User: convert.OwnUser(output.User, output.FederatedID)}, nil
}
