//go:build e2e && !kind

package e2e_test

import "time"

// settleFor bounds how long a test waits for something that happens on its own
// — a replication pass, a stream delivering a change.
//
// The compose federation runs its worker every five seconds, which is a
// development interval chosen so that a demonstration does not have to wait
// half a minute for a change to cross, so this is a few passes and not a guess.
// The cluster runs the production one, and settle_kind_test.go is that number.
const settleFor = 30 * time.Second
