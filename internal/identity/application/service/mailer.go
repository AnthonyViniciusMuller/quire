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

// Mailer delivers to the address on record.
//
// It is a port with, for now, one adapter that does not deliver. C13 in
// docs/tcc-corrections.md is why: RF09 requires a credential to reach a
// reader's address, and the deployment the thesis describes holds no component
// that can send one. Naming the port is what makes the gap visible and the
// eventual transport a single package.
type Mailer interface {
	// SendPasswordRecovery delivers the first half of UC08.
	//
	// A failure is reported, and the caller deliberately does not pass it on:
	// the reply to a recovery request is the same whether or not the address is
	// registered here, and an error that only ever happened for addresses that
	// exist would undo that.
	SendPasswordRecovery(ctx context.Context, message RecoveryMessage) error
}
