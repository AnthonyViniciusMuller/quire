package service

import "context"

// Transaction runs work as one unit: everything inside it commits together or
// none of it does.
//
// Logging in is why the port exists. It binds a device and issues the
// credential that device presents afterwards, and a node that wrote the first
// and failed at the second would have bound an appliance to an account that
// cannot use it — a row nobody would think to look for and nothing would clean
// up.
//
// The context is both the parameter and the mechanism: what identifies the
// transaction travels in the one handed to fn, so the repositories called
// inside join it without being told, and the ones called outside do not.
type Transaction interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}
