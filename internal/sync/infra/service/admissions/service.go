// Package admissions tells a node that it holds a reader, out of what the
// federation and identity slices know about them.
//
// It is the adapter of the Admissions port the delivery pass holds, composed
// of two of the federation slice's ports: Readers, which describes the reader
// and their devices, and Peers, which makes the call. It is wired in
// cmd/quired where the containers meet, on the pattern the replicas adapter
// beside it set.
//
// # What it remembers
//
// A pass offers a reader's changes to a node on every tick there is something
// owed, and telling the node who the reader is on every one of them would
// double the calls replication makes. The adapter therefore keeps, per node
// and reader, what the node was last told — the devices and how much the
// reader allowed — and calls the node only when that has changed, or when
// the last call did not succeed. The memory is the process's: a node that
// restarts tells every peer once more, which costs one call per pair and is
// what makes the memory safe to lose.
package admissions

import (
	"context"
	"slices"
	"strings"
	"sync"
	"uuid"

	federationservice "github.com/anthonyvsmuller/quire/internal/federation/application/service"
	federationreplica "github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/sync/application/service"
)

// Service admits readers to the nodes they authorized.
type Service struct {
	readers        federationservice.Readers
	peers          federationservice.Peers
	authorizations federationreplica.Repository

	mu   sync.Mutex
	told map[pair]string
}

// Service satisfies the port the use cases hold.
var _ service.Admissions = (*Service)(nil)

// pair is one node and one reader.
type pair struct {
	server uuid.UUID
	user   uuid.UUID
}

// New returns the adapter over the federation slice's ports.
func New(
	readers federationservice.Readers,
	peers federationservice.Peers,
	authorizations federationreplica.Repository,
) *Service {
	return &Service{readers: readers, peers: peers, authorizations: authorizations, told: map[pair]string{}}
}

// Admit tells the node about the reader, unless it was told the same thing
// already.
func (s *Service) Admit(ctx context.Context, serverID, userID uuid.UUID) error {
	reader, devices, err := s.readers.Describe(ctx, userID)
	if err != nil {
		return err
	}

	granted, err := s.authorizations.GetByPair(ctx, userID, serverID)
	if err != nil {
		return err
	}

	admission := &federationservice.Admission{
		Reader:          *reader,
		Devices:         devices,
		ReplicatesFiles: granted.ReplicatesFiles,
	}

	key := pair{server: serverID, user: userID}
	now := describe(admission)

	s.mu.Lock()
	last, told := s.told[key]
	s.mu.Unlock()

	if told && last == now {
		return nil
	}

	if err := s.peers.Admit(ctx, serverID, admission); err != nil {
		return err
	}

	s.mu.Lock()
	s.told[key] = now
	s.mu.Unlock()

	return nil
}

// describe renders what a node would be told, so that two admissions that
// would say the same thing compare equal.
func describe(admission *federationservice.Admission) string {
	ids := make([]string, 0, len(admission.Devices))
	for _, appliance := range admission.Devices {
		ids = append(ids, appliance.ID.String()+"="+appliance.Name+"/"+appliance.Platform)
	}

	slices.Sort(ids)

	files := "metadata"
	if admission.ReplicatesFiles {
		files = "files"
	}

	return admission.Reader.LocalName + "|" + admission.Reader.DisplayName + "|" + files + "|" + strings.Join(ids, ",")
}
