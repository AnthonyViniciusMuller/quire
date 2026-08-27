//go:build integration

// Package integration_test exercises the identity slice against a real
// PostgreSQL: the statements, the indexes, the transactions and the whole gRPC
// surface over a real listener.
//
// What it covers is what a unit test cannot. The doubles in
// internal/identity/application/apptest imitate the two uniqueness rules of
// RN09 and the consume-once semantics of a credential, and an imitation is
// exactly as good as the reading it was written from — these tests are what
// checks that reading against the database.
//
// The database is supplied rather than started. QUIRE_TEST_DATABASE_URL has to
// point at one, and the tests fail rather than skip when it does not: a skipped
// test is a test that is not run, and this is the suite most likely to be the
// one nobody noticed had stopped running. The build tag is what keeps it out of
// `go test ./...`, so it cannot fail anybody by accident either.
//
// The suite owns that database. It drops every schema the node declares before
// it applies them, which is what makes a run repeatable — so the variable must
// point at a throwaway one, and `make test-integration` says how to get one.
package integration_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
)

// databaseURLVariable names the database the suite runs against.
const databaseURLVariable = "QUIRE_TEST_DATABASE_URL"

// migrationsDirectory holds the schema, relative to this package.
const migrationsDirectory = "../../migrations"

// testServerName is the node these tests are the origin server of.
const testServerName = "quire-a.example"

// pool is opened once for the suite. Every test shares it and resets the tables
// it uses, which is cheaper than a database each and is what makes the
// statements run against the same catalogue the node runs against.
var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// run opens the database, applies the schema, and runs the suite.
//
// It is separate from TestMain so that the deferred close happens: os.Exit does
// not run deferred functions, and a pool left open holds connections the next
// run would compete with.
func run(m *testing.M) int {
	databaseURL := os.Getenv(databaseURLVariable)
	if databaseURL == "" {
		panic(databaseURLVariable + " is not set. These tests need a PostgreSQL to run against; " +
			"`make test-integration` says how to get a throwaway one.")
	}

	ctx := context.Background()

	opened, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		panic("connecting to " + databaseURLVariable + ": " + err.Error())
	}

	defer opened.Close()

	if err := opened.Ping(ctx); err != nil {
		panic("the database at " + databaseURLVariable + " is not answering: " + err.Error())
	}

	pool = opened

	if err := applySchema(ctx, opened); err != nil {
		panic("applying the schema: " + err.Error())
	}

	return m.Run()
}

// schemas are the ones the node owns, and the ones this suite drops before it
// begins.
const schemas = "federation, identity, library, reading, sync"

// applySchema drops what the node owns and runs every .up.sql in order.
//
// It reads the files rather than driving golang-migrate, which the Makefile
// pins for operators. What a test needs is the schema, not the version table,
// and a suite that depended on the migration tool would fail for reasons that
// have nothing to do with what it is testing.
//
// Dropping first is what makes a run repeatable and what makes a changed
// migration take effect. It is also why the variable is named for a test
// database: this suite owns what it points at, and will destroy every schema
// the node declares.
func applySchema(ctx context.Context, opened *pgxpool.Pool) error {
	if _, err := opened.Exec(ctx, "DROP SCHEMA IF EXISTS "+schemas+" CASCADE"); err != nil {
		return err
	}

	entries, err := filepath.Glob(filepath.Join(migrationsDirectory, "*.up.sql"))
	if err != nil {
		return err
	}

	sort.Strings(entries)

	for _, entry := range entries {
		statements, readErr := os.ReadFile(entry)
		if readErr != nil {
			return readErr
		}

		if _, execErr := opened.Exec(ctx, string(statements)); execErr != nil {
			return execErr
		}
	}

	return nil
}

// reset empties the tables of the identity and federation slices, so that each
// test starts from a catalogue with nothing in it.
//
// federation.servers is the root of the cascade: a reader references it, and
// their devices and credentials reference them.
func reset(t *testing.T) {
	t.Helper()

	_, err := pool.Exec(t.Context(), "TRUNCATE federation.servers CASCADE")
	if err != nil {
		t.Fatalf("resetting the database: %v", err)
	}
}

// nodeConfig is a node configured as a development one, with a signing key
// generated for this run.
func nodeConfig(t *testing.T) *config.Config {
	t.Helper()

	base, err := url.Parse("http://" + testServerName)
	if err != nil {
		t.Fatalf("parsing the base url: %v", err)
	}

	return &config.Config{
		Environment: config.Development,
		Server: config.Server{
			Name:                  testServerName,
			BaseURL:               base,
			GRPCAddress:           "127.0.0.1:0",
			HTTPAddress:           "127.0.0.1:0",
			GRPCAdvertisedAddress: testServerName + ":9090",
			ShutdownTimeout:       5 * time.Second,
		},
		Database: config.Database{URL: config.Secret(os.Getenv(databaseURLVariable))},
		Auth: config.Auth{
			PrivateKeyPEM: signingKey(t),
			KeyID:         "signing-key-1",
			Issuer:        "http://" + testServerName,
			// Long enough that no test in this suite watches one expire, and
			// short enough that the ordering still says which is which.
			AccessTokenTTL:   15 * time.Minute,
			RefreshTokenTTL:  720 * time.Hour,
			PasswordResetTTL: time.Hour,
			// The cheapest bcrypt accepts. These tests are about the
			// statements, and a work factor of twelve would spend minutes
			// proving something the hashing service already has a test for.
			BcryptCost: 4,
		},
	}
}

// signingKey is a fresh P-256 key in the encoding the node reads.
func signingKey(t *testing.T) config.Secret {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a signing key: %v", err)
	}

	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encoding the signing key: %v", err)
	}

	return config.Secret(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
}

// contains reports whether haystack holds needle, for the assertions that are
// about a message rather than about a code.
func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }
