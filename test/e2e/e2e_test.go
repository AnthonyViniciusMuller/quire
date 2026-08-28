//go:build e2e

// Package e2e_test drives the whole system the way a reader's device does:
// through the client of internal/client, against the two federated nodes of
// deploy/docker, over the network, with nothing stubbed.
//
// What it covers is what the integration suite cannot. That suite builds the
// node's containers in the test process and calls them over a listener it
// opened, which is the right level for a statement, an index or a
// reconciliation; it cannot show a change made on one device appearing on
// another, because both devices would be the same process holding the same
// clock, and it cannot show two nodes agreeing about anything, because there is
// only ever one.
//
// The federation is supplied rather than started, on the same terms as the
// integration suite's database: `make dev-up` brings it up, the variables below
// point at it, and the tests fail rather than skip when they do not. A skipped
// test is a test that is not run, and this is the suite most likely to be the
// one nobody noticed had stopped running. The build tag is what keeps it out of
// `go test ./...`.
//
// Every test registers a reader of its own, with a name nothing else will use,
// and never resets anything. The federation is long-lived and shared — it is
// what a demonstration runs on too — so a suite that emptied it would be a
// suite nobody could run twice in an afternoon, and a reader per test is what
// makes the runs independent without it.
package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/anthonyvsmuller/quire/internal/client"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// The variables naming the federation these tests run against. `make test-e2e`
// sets them to what `make dev-up` starts.
const (
	nodeAAddressVariable     = "QUIRE_TEST_NODE_A"
	nodeACAVariable          = "QUIRE_TEST_NODE_A_CA"
	nodeADatabaseURLVariable = "QUIRE_TEST_NODE_A_DATABASE_URL"
	nodeBAddressVariable     = "QUIRE_TEST_NODE_B"
	nodeBCAVariable          = "QUIRE_TEST_NODE_B_CA"
	nodeBDatabaseURLVariable = "QUIRE_TEST_NODE_B_DATABASE_URL"
)

// thePassword is what every reader in this suite registers with. It is the one
// secret here and it protects nothing: the accounts live for the length of a
// test run, on a federation that exists on one machine.
const thePassword = "correct horse battery staple"

// settleFor bounds how long a test waits for something that happens on its own
// — a replication pass, a stream delivering a change. The federation runs its
// worker every five seconds, so this is a few passes and not a guess.
const settleFor = 30 * time.Second

// node is one of the two nodes the local federation runs.
type node struct {
	// domain is what the node calls itself inside the federation, and the
	// second half of every identifier it hosts.
	domain string
	// address is where this suite reaches it, which is the port compose
	// published on the host and never the one the node answers on inside its
	// own network.
	address string
	// ca is the certificate the node presents. It is self-signed, because two
	// nodes share no authority and what identifies one is the key it published
	// (C12) — so a client outside the network verifies it against the file
	// itself.
	ca string
	// databaseURL is the node's own PostgreSQL, and it is here for exactly one
	// thing: the state a peer has to hold before it may be replicated to, and
	// which the contract has no call for. C22 in docs/tcc-corrections.md is
	// that finding; replication_test.go is where it is used and says so again
	// at the point of use.
	databaseURL string
}

// The two nodes, read once from the environment.
var (
	nodeA node
	nodeB node
)

func TestMain(m *testing.M) {
	nodeA = node{
		domain:      "quire-a.example",
		address:     requiredEnv(nodeAAddressVariable),
		ca:          requiredEnv(nodeACAVariable),
		databaseURL: requiredEnv(nodeADatabaseURLVariable),
	}
	nodeB = node{
		domain:      "quire-b.example",
		address:     requiredEnv(nodeBAddressVariable),
		ca:          requiredEnv(nodeBCAVariable),
		databaseURL: requiredEnv(nodeBDatabaseURLVariable),
	}

	for _, answering := range []node{nodeA, nodeB} {
		if err := answering.reachable(); err != nil {
			panic(fmt.Sprintf("the node at %s is not answering: %v\nrun make dev-up", answering.address, err))
		}
	}

	os.Exit(m.Run())
}

// requiredEnv returns the variable, or stops the suite saying which one is
// missing. A default here would be a suite that silently ran against something
// nobody meant it to.
func requiredEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic(name + " is not set: run the suite through `make test-e2e`")
	}

	return value
}

// reachable asks the node something it will refuse, because a refusal is the
// node and only the transport can fail differently.
//
// It is also what checks the certificate: a node presenting one this suite
// cannot verify fails here, with the reason, rather than in whichever test
// happened to run first.
func (n node) reachable() error {
	transport, err := credentials.NewClientTLSFromFile(n.ca, "")
	if err != nil {
		return fmt.Errorf("reading %s: %w", n.ca, err)
	}

	connection, err := grpc.NewClient(n.address, grpc.WithTransportCredentials(transport))
	if err != nil {
		return err
	}

	defer func() { _ = connection.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), settleFor)
	defer cancel()

	_, err = quirev1.NewAuthServiceClient(connection).Login(ctx, &quirev1.LoginRequest{
		LoginId:  &quirev1.LoginRequest_LocalName{LocalName: "nobody"},
		Password: "nothing",
		Device:   &quirev1.DeviceBinding{Name: "e2e", Platform: "test"},
	})

	if status.Code(err) == codes.Unauthenticated {
		return nil
	}

	return err
}

// reader is an account registered for one test.
type reader struct {
	localName string
	email     string
	// on is the node hosting them, which is the only node that can
	// authenticate them (RN08).
	on node
}

// newReader registers a reader nothing else will collide with.
//
// The name carries the run rather than the test, because a local name is unique
// per origin server and this federation outlives the process: two runs of the
// same test are two readers, and neither has to clean up after the other.
func newReader(t *testing.T, on node) *reader {
	t.Helper()

	local := "e2e-" + token(t)
	who := &reader{localName: local, email: local + "@example.org", on: on}

	connection := open(t, on, filepath.Join(t.TempDir(), "registration.json"), false)

	if _, err := connection.Register(t.Context(), &client.Registration{
		LocalName:   who.localName,
		DisplayName: "A reader of the end-to-end suite",
		Email:       who.email,
		Password:    thePassword,
	}); err != nil {
		t.Fatalf("registering %s on %s: %v", who.localName, on.domain, err)
	}

	return who
}

// device is one appliance of one reader: a client, and the file that makes it
// the same device between two of them.
type device struct {
	*client.Client

	name      string
	statePath string
	on        node
}

// newDevice binds a device to the reader and logs it in.
//
// The name is what a reader would see in `quirectl device list`, and it is
// carried here so that a failure names the device that caused it rather than a
// uuid nobody can place.
func newDevice(t *testing.T, on node, who *reader, name string) *device {
	t.Helper()

	appliance := &device{
		name:      name,
		statePath: filepath.Join(t.TempDir(), name+".json"),
		on:        on,
	}

	appliance.Client = open(t, on, appliance.statePath, false)

	if _, err := appliance.Login(t.Context(), &client.Credentials{
		LocalName:      who.localName,
		Password:       thePassword,
		DeviceName:     name,
		DevicePlatform: "e2e",
	}); err != nil {
		t.Fatalf("logging %s in as %s: %v", name, who.localName, err)
	}

	return appliance
}

// disconnect puts the device out of reach of its node, which is the state
// UC11 is about: it keeps its identity, its clock and everything it has seen,
// and every change it makes from now on is stamped here and queued.
func (d *device) disconnect(t *testing.T) {
	t.Helper()

	_ = d.Close()
	d.Client = open(t, d.on, d.statePath, true)
}

// reconnect brings it back. What it holds is what the file holds, which is the
// whole point of a device keeping one.
func (d *device) reconnect(t *testing.T) {
	t.Helper()

	_ = d.Close()
	d.Client = open(t, d.on, d.statePath, false)
}

// open builds a client for one device against one node.
func open(t *testing.T, on node, statePath string, offline bool) *client.Client {
	t.Helper()

	connection, err := client.Open(client.Options{
		Address:       on.address,
		StatePath:     statePath,
		Offline:       offline,
		CACertificate: on.ca,
	})
	if err != nil {
		t.Fatalf("opening a client for %s: %v", on.domain, err)
	}

	t.Cleanup(func() { _ = connection.Close() })

	return connection
}

// token is a value nothing else in this federation will hold.
func token(t *testing.T) string {
	t.Helper()

	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("generating a name: %v", err)
	}

	return hex.EncodeToString(value)
}

// drain pulls until the node has nothing left, which is what a device that has
// been away does before it can be said to have caught up.
func drain(t *testing.T, appliance *device) []*quirev1.Operation {
	t.Helper()

	var collected []*quirev1.Operation

	for {
		report, err := appliance.Pull(t.Context(), 0)
		if err != nil {
			t.Fatalf("%s pulling from %s: %v", appliance.name, appliance.on.domain, err)
		}

		collected = append(collected, report.Operations...)

		if !report.HasMore {
			return collected
		}
	}
}

// push hands over what the device authored while it was away, and fails on any
// verdict but the ones that mean the node has it.
//
// Applied, duplicate and superseded are all the node having the change:
// superseded is a change that lost the merge and is kept, because a later node
// has to reach the same conclusion from the same history. A rejection is the
// only answer that loses one, and a test that let it pass would be a test that
// checks convergence between a node and a device that gave up.
func push(t *testing.T, appliance *device) client.PushReport {
	t.Helper()

	report, err := appliance.Push(t.Context())
	if err != nil {
		t.Fatalf("%s pushing to %s: %v", appliance.name, appliance.on.domain, err)
	}

	for _, result := range report.Results {
		if result.GetOutcome() == quirev1.OperationOutcome_OPERATION_OUTCOME_REJECTED {
			t.Fatalf("%s had a change refused: %s", appliance.name, result.GetDetail())
		}
	}

	return report
}

// title is what a work is called on the node, which is what most of these tests
// compare: it is one field, it is per-field last-writer-wins, and two devices
// writing it is the smallest conflict the system can have.
func title(t *testing.T, appliance *device, work uuid.UUID) string {
	t.Helper()

	found, err := appliance.GetEbook(t.Context(), work)
	if err != nil {
		t.Fatalf("%s reading the work: %v", appliance.name, err)
	}

	return found.GetTitle()
}

// gone reports whether the node answered that there is no such record, which is
// what a tombstoned work answers a reader who asks for it.
func gone(err error) bool { return status.Code(err) == codes.NotFound }
