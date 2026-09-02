package service

import (
	"context"
	"uuid"
)

// Reader is a reader as a replica holds them: the identifier every table of
// theirs hangs off, and the two names. No address and no password, which is
// what makes a replicated reader one this node does not authenticate (C03,
// RN08).
type Reader struct {
	ID          uuid.UUID
	LocalName   string
	DisplayName string
}

// Device is a device as a replica holds it: the identifier every vector clock
// entry is keyed by, and what the reader called it.
type Device struct {
	ID       uuid.UUID
	Name     string
	Platform string
}

// Readers is the identity slice as this one sees it: the readers a peer may
// tell this node about, and nothing else about them.
//
// It is a port for the reason the reading slice's Works is one. A replica
// holds a row for every reader it replicates and for every device that
// authored anything of theirs — sync.operations references identity.devices,
// and a peer holding the reader without their devices refuses the whole batch
// — and those rows belong to the identity slice. The adapter in
// internal/federation/infra/service writes them through the identity slice's
// own repositories, so that what a replicated reader is stays in the package
// that defines one, and it is wired in cmd/quired where the containers meet.
type Readers interface {
	// Admit records the reader as replicated from the origin, and the devices
	// as theirs, creating what is missing and leaving what is there.
	//
	// It refuses, with errs.KindPermissionDenied, a reader this node holds
	// under another origin — including one it hosts itself — and a device
	// that belongs to another reader: the identifiers are what the peer
	// claims, and a claim over a row that already has an owner is not one a
	// peer gets to make. A name it cannot parse is errs.KindInvalidArgument.
	Admit(ctx context.Context, originServerID uuid.UUID, reader *Reader, devices []Device) error
}
