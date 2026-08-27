package service

import "context"

// Transaction runs work as one unit: everything inside it commits together or
// none of it does.
//
// In this slice it is not there to make two writes atomic — most of the calls
// write one row — but to make a read and a write atomic against each other.
// Forgetting a node and stopping one both consult the authorizations and then
// write the catalogue, and a reader authorizing that node in between would be
// invisible to the check and lost to the cascade. Those calls take the row
// lock of server.Repository.GetByIDForUpdate, and a lock outside a transaction
// is released with the statement that took it, which is to say immediately.
//
// The context is both the parameter and the mechanism: what identifies the
// transaction travels in the one handed to fn, so the repositories called
// inside join it without being told, and the ones called outside do not.
type Transaction interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}
