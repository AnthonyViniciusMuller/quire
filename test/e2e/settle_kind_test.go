//go:build e2e && kind

package e2e_test

import "time"

// settleFor bounds how long a test waits for something that happens on its own,
// in the cluster rather than in the compose federation.
//
// It is five times the other one, and the multiplier is not caution. The
// manifests set QUIRE_FEDERATION_REPLICATION_INTERVAL to thirty seconds because
// they declare the production profile and thirty is what the node defaults to;
// the compose file overrides it to five so that a demonstration does not wait.
// A change that missed the tick it was written before therefore waits a whole
// interval, and a test allowing only one interval would fail on the pass it was
// unlucky about.
//
// This is the whole of what the kind build tag changes. Everything else in the
// suite is the same suite: the same clients, the same assertions, and the same
// federation seen through a mesh instead of through a bridge network.
const settleFor = 150 * time.Second
