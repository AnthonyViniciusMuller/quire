package addtocollection

import "uuid"

// Input files one work under one grouping.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// DeviceID is the appliance making the filing, from the token. It is what
	// the register's revision names.
	DeviceID uuid.UUID
	// EbookID is the work.
	EbookID uuid.UUID
	// CollectionID is the grouping.
	CollectionID uuid.UUID
}

// Output is empty. The contract reports nothing here, and there is nothing to
// report: the call is idempotent, so its result is the same whether or not it
// changed anything.
type Output struct{}
