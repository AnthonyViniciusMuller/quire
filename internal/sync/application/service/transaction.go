package service

import "context"

// Transaction runs work as one unit: everything inside it commits together or
// none of it does.
//
// This slice needs it for the reason C08 gives, which is stronger than the one
// the library slice has. Storing an operation allocates this node's position
// for it from a row lock, and the lock is what makes the order of the numbers
// the order of the commits; an allocation that committed separately from the
// insert it numbered would release the lock before the operation was visible,
// and a device that had already read past the number would never come back for
// it.
//
// It is also what makes an operation and its effect on the record one write. A
// node that stored the operation and failed to apply it would answer the next
// pull with a change no record reflects, and a node that applied it without
// storing it would apply it again on the next delivery.
//
// The unit is one operation and not one batch, and that is deliberate. A
// rejected operation has to leave nothing behind while the operations around it
// stand, and a batch in one transaction cannot offer that: a statement PostgreSQL
// refuses aborts the whole transaction, so one refused change would take a
// reader's whole push with it.
type Transaction interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}
