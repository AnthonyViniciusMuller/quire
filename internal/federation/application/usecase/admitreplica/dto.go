package admitreplica

import "github.com/anthonyvsmuller/quire/internal/federation/application/service"

// Input is what the origin told this node, and the pin it said it with.
type Input struct {
	// Pin is the public key digest of the certificate the caller presented,
	// which is the credential of a peer-facing call. The controller reads it
	// off the connection; nothing in the request names the caller.
	Pin string

	// Reader is who the origin says authorized this node.
	Reader service.Reader

	// Devices is every device the reader has bound there.
	Devices []service.Device

	// ReplicatesFiles is whether the permission covers the files as well as
	// the metadata, as the reader granted it.
	ReplicatesFiles bool
}

// Output is empty: what the call records is not the caller's to read back.
type Output struct{}
