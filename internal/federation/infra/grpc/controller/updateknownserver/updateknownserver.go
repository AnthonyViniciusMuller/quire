// Package updateknownserver serves FederationService.UpdateKnownServer (UC12,
// update).
package updateknownserver

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/updateserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/identifier"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opHandle is the operation reported by this file, in the form the errs
// package expects.
const opHandle = "federation/updateknownserver: handle"

// CodeUnwritableField is a mask naming a field this call does not write.
const CodeUnwritableField = "unwritable_field"

// pathActive is the one path the mask may carry. Everything else in a
// catalogue row was learned from the node itself and is refreshed rather than
// typed.
const pathActive = "active"

// UpdateKnownServer serves the call.
type UpdateKnownServer struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(update command.Usecase[usecase.Input, usecase.Output]) *UpdateKnownServer {
	return &UpdateKnownServer{usecase: update}
}

// Handle applies the field the mask names.
//
// A path this call does not write is refused rather than ignored, as it is on
// the reader's own record: a client that asked to change a pin should be told
// that it cannot, not answered with a row where it did not change — and a pin
// that could be typed would not be a pin.
//
// An empty mask is refused too. On a message whose only writable field is a
// boolean, an absent mask and a mask naming `active` would otherwise differ
// only in that the first silently means false.
func (c *UpdateKnownServer) Handle(
	ctx context.Context,
	request *quirev1.UpdateKnownServerRequest,
) (*quirev1.UpdateKnownServerResponse, error) {
	serverID, err := identifier.Server(request.GetServerId())
	if err != nil {
		return nil, err
	}

	paths := request.GetUpdateMask().GetPaths()
	if len(paths) == 0 {
		return nil, c.unwritable("the mask must name active, which is the only field this call writes")
	}

	for _, path := range paths {
		if path != pathActive {
			return nil, c.unwritable("only active may be changed; everything else is what the node said " +
				"about itself, and is refreshed rather than typed")
		}
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		ServerID: serverID,
		Active:   request.GetServer().GetActive(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.UpdateKnownServerResponse{Server: convert.Server(output.Server)}, nil
}

// unwritable is the answer to a mask this call cannot apply.
func (c *UpdateKnownServer) unwritable(reason string) error {
	return errs.New(errs.KindInvalidArgument, "that is not a field this call writes").
		WithOp(opHandle).
		WithCode(CodeUnwritableField).
		WithField("update_mask", reason)
}
