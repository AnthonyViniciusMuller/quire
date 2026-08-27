// Package updateprogress records where the calling device has reached in a
// work (UC05, RF02).
//
// One row per work and device, created on first use and overwritten afterwards,
// as Quadro 21 specifies. The device is never a parameter of the call: it comes
// from the token, and the contract leaves the field out of the request for the
// reason RN10 gives — a request that could name a device would be a request
// that could move another device's bookmark.
//
// Nothing here reconciles. A progress row has one writer, so its versions are
// totally ordered by that device's own counter and two of them can never be
// concurrent (C05); what looks like a conflict on this entity — two devices at
// two places in one book — is two rows, and which of them to show the reader is
// the client's decision (RN01).
package updateprogress

import (
	"context"
	"errors"
	"time"

	"github.com/anthonyvsmuller/quire/internal/reading/application/service"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// UpdateProgress records reading positions.
type UpdateProgress struct {
	positions progress.Repository
	works     service.Works
	clock     service.Clock
}

// UpdateProgress satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*UpdateProgress)(nil)

// New returns the use case over its dependencies.
func New(positions progress.Repository, works service.Works, clock service.Clock) *UpdateProgress {
	return &UpdateProgress{positions: positions, works: works, clock: clock}
}

// Execute moves the device's position, recording it the first time.
//
// The read and the write are not one transaction, and not because it would be
// awkward: there is no row to lock the first time through, so a transaction
// would serialize nothing that the unique constraint does not already settle.
// What settles it is the constraint plus one retry — two calls from the same
// device crossing on a flaky network is the ordinary way it happens, and the
// loser reads the row the winner wrote and moves it.
//
// One retry is enough, and that is a property of the table rather than an
// optimism. No statement removes a progress row, so a pair that exists once
// exists for as long as the work does; the only way the second read can fail is
// that the work itself was deleted, which is the answer the caller should get.
//
// The alternative, one INSERT ... ON CONFLICT DO UPDATE, was not taken. The new
// version is derived from the one the row carries — tick that clock, step past
// that timestamp — so a SET clause that computed it would be a second copy of
// C01's rule in a language where it could not be tested against the first.
func (u *UpdateProgress) Execute(ctx context.Context, input Input) (Output, error) {
	if err := u.works.Visible(ctx, input.EbookID, input.UserID); err != nil {
		return Output{}, err
	}

	position, err := parse(&input)
	if err != nil {
		return Output{}, err
	}

	at := u.clock.Now()

	stored, err := u.positions.GetByPair(ctx, input.EbookID, input.DeviceID)

	switch {
	case err == nil:
		return u.move(ctx, stored, position, at)

	case errors.Is(err, errs.KindNotFound):
		return u.record(ctx, &input, position, at)

	default:
		return Output{}, err
	}
}

// record writes the first position of this device in this work, and answers a
// crossing call by reading what it wrote and moving that.
func (u *UpdateProgress) record(
	ctx context.Context, input *Input, position *progress.Position, at time.Time,
) (Output, error) {
	created, err := progress.New(input.EbookID, input.DeviceID, position, at)
	if err != nil {
		return Output{}, err
	}

	err = u.positions.Create(ctx, created)

	switch {
	case err == nil:
		return Output{Progress: created}, nil

	case errors.Is(err, errs.KindAlreadyExists):
		stored, readErr := u.positions.GetByPair(ctx, input.EbookID, input.DeviceID)
		if readErr != nil {
			return Output{}, readErr
		}

		return u.move(ctx, stored, position, at)

	default:
		return Output{}, err
	}
}

// move records where the device has reached now, on the row it already has.
func (u *UpdateProgress) move(
	ctx context.Context, stored *progress.Progress, position *progress.Position, at time.Time,
) (Output, error) {
	if err := stored.MoveTo(position, at); err != nil {
		return Output{}, err
	}

	if err := u.positions.Update(ctx, stored); err != nil {
		return Output{}, err
	}

	return Output{Progress: stored}, nil
}

// parse turns what the client sent into the position the entity takes.
func parse(input *Input) (*progress.Position, error) {
	place, err := locator.Parse(input.Locator)
	if err != nil {
		return nil, err
	}

	percent, err := progress.ParsePercent(input.Percent)
	if err != nil {
		return nil, err
	}

	return &progress.Position{Locator: place, Percent: percent}, nil
}
