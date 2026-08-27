package service

import "context"

// Transaction runs work as one unit: everything inside it commits together or
// none of it does.
//
// This slice needs it for writes that are one change to a reader and two rows
// in the database. Deleting a work tombstones the work and every filing of it,
// and a node that committed the first without the second would show the work on
// a shelf it is no longer in — on this node until somebody noticed, and on
// every peer for as long as they had not heard otherwise, because the two
// tombstones replicate independently.
//
// It also makes a read and a write atomic against each other. Filing a work
// under a grouping reads the grouping and then writes a row that references it,
// and a deletion of that grouping arriving in between would be invisible to the
// read; that call takes the row lock of collection.Repository.GetByIDForUpdate,
// and a lock outside a transaction is released with the statement that took it.
//
// The context is both the parameter and the mechanism: what identifies the
// transaction travels in the one handed to fn, so the repositories called
// inside join it without being told, and the ones called outside do not.
type Transaction interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}
