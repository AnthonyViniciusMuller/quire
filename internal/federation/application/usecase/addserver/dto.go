package addserver

import "github.com/anthonyvsmuller/quire/internal/federation/domain/server"

// Input is a domain the reader wants the node to know.
//
// It is the only field, and the contract has no other: a reader who could type
// the base URL or the pin by hand could also type the wrong one, and the pin
// is the whole trust anchor of RNF08. Everything but the domain is what the
// node says about itself.
type Input struct {
	// Domain is the authority to discover, as the reader typed it.
	Domain string
}

// Output is the node as the catalogue now holds it.
type Output struct {
	// Server is the row that was written.
	Server *server.Server
}
