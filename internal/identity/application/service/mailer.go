package service

import (
	"context"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
)

// RecoveryMessage is what a reader has to receive in order to set a new
// password: the credential, who it is for, and how long they have.
type RecoveryMessage struct {
	// Email is the address on record, which is the only channel a reader who
	// has lost their password still has.
	Email user.Email
	// DisplayName is what to call them.
	DisplayName user.DisplayName
	// Token is the credential itself, which exists nowhere else — the node
	// stored only its digest, and cannot send it a second time.
	Token string
	// ExpiresAt is when it stops working, so that the message can say so.
	ExpiresAt time.Time
}

// EmailChangedMessage is what the *previous* address has to receive when the
// address on record is replaced.
//
// It is the half of C14 that survives a compromise. Asking for the password
// stops a device left unlocked for a minute; it does not stop somebody who
// learned the password, and for them this notice is how the reader finds out at
// all — sent to the address they still hold, about a change made to the one
// they no longer do.
type EmailChangedMessage struct {
	// PreviousEmail is where this goes: the address that was on record until
	// the change, and the only one the reader is still known to read.
	PreviousEmail user.Email
	// NewEmail is what it was changed to, and it is named rather than withheld.
	// A reader told only that their address changed cannot tell whether it was
	// them; and whoever reads the previous mailbox either is the reader, or
	// already held the channel UC08 recovers through and could have taken the
	// account without this.
	NewEmail user.Email
	// DisplayName is what to call them.
	DisplayName user.DisplayName
	// ChangedAt is when it happened, which is what makes the notice actionable:
	// a reader who was not at their device then knows what they are looking at.
	ChangedAt time.Time
}

// Mailer delivers to an address a reader holds.
//
// C13 in docs/tcc-corrections.md is why this is a port and was for a while a
// port with only an adapter that did not deliver: RF09 requires a credential to
// reach a reader's address, and the deployment the thesis describes named no
// component that could send one. The transport is
// internal/identity/infra/service/smtp, and every adapter of this port is
// reached through the queue in internal/identity/infra/service/deferred.
type Mailer interface {
	// SendPasswordRecovery delivers the first half of UC08.
	//
	// A failure is reported, and the caller deliberately does not pass it on:
	// the reply to a recovery request is the same whether or not the address is
	// registered here, and an error that only ever happened for addresses that
	// exist would undo that.
	SendPasswordRecovery(ctx context.Context, message RecoveryMessage) error

	// SendEmailChanged tells the previous address that it is no longer the one.
	//
	// A failure is reported and, again, not passed on to the caller: the
	// address has already changed by the time this runs, and answering the
	// reader with an error would tell them a write failed that did not.
	SendEmailChanged(ctx context.Context, message EmailChangedMessage) error
}
