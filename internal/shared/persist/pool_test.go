package persist_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// databaseConfig returns a valid database section, which a test then varies in
// the one field it is about.
func databaseConfig() config.Database {
	return config.Database{
		URL:                   "postgres://quire:quire@localhost:5432/quire?sslmode=disable",
		MaxConnections:        10,
		MinConnections:        2,
		MaxConnectionLifetime: time.Hour,
		MaxConnectionIdleTime: 30 * time.Minute,
		ConnectTimeout:        10 * time.Second,
	}
}

func TestNewPoolConfigAppliesTheConfiguredLimits(t *testing.T) {
	t.Parallel()

	cfg := databaseConfig()

	poolConfig, err := persist.NewPoolConfig(cfg)
	if err != nil {
		t.Fatalf("NewPoolConfig returned %v", err)
	}

	if got, want := poolConfig.MaxConns, int32(cfg.MaxConnections); got != want {
		t.Errorf("MaxConns = %d, want %d", got, want)
	}

	if got, want := poolConfig.MinConns, int32(cfg.MinConnections); got != want {
		t.Errorf("MinConns = %d, want %d", got, want)
	}

	// The warm connections are kept idle, not merely open, so that the first
	// request after a quiet period does not pay for a handshake.
	if got, want := poolConfig.MinIdleConns, int32(cfg.MinConnections); got != want {
		t.Errorf("MinIdleConns = %d, want %d", got, want)
	}

	if got, want := poolConfig.MaxConnLifetime, cfg.MaxConnectionLifetime; got != want {
		t.Errorf("MaxConnLifetime = %s, want %s", got, want)
	}

	if got, want := poolConfig.MaxConnIdleTime, cfg.MaxConnectionIdleTime; got != want {
		t.Errorf("MaxConnIdleTime = %s, want %s", got, want)
	}

	if got, want := poolConfig.ConnConfig.ConnectTimeout, cfg.ConnectTimeout; got != want {
		t.Errorf("ConnectTimeout = %s, want %s", got, want)
	}
}

func TestNewPoolConfigJittersTheConnectionLifetime(t *testing.T) {
	t.Parallel()

	poolConfig, err := persist.NewPoolConfig(databaseConfig())
	if err != nil {
		t.Fatalf("NewPoolConfig returned %v", err)
	}

	// Without jitter every connection opened during a rollout retires in the
	// same instant, emptying the pool under load.
	if poolConfig.MaxConnLifetimeJitter <= 0 {
		t.Fatalf("MaxConnLifetimeJitter = %s, want a positive duration", poolConfig.MaxConnLifetimeJitter)
	}

	if poolConfig.MaxConnLifetimeJitter >= poolConfig.MaxConnLifetime {
		t.Errorf("MaxConnLifetimeJitter = %s, want less than the lifetime of %s",
			poolConfig.MaxConnLifetimeJitter, poolConfig.MaxConnLifetime)
	}
}

func TestNewPoolConfigRejectsAMalformedURL(t *testing.T) {
	t.Parallel()

	cfg := databaseConfig()
	cfg.URL = "not-a-connection-string://"

	_, err := persist.NewPoolConfig(cfg)
	if err == nil {
		t.Fatal("NewPoolConfig accepted a malformed URL")
	}

	if !errors.Is(err, errs.KindInternal) {
		t.Errorf("kind = %s, want %s", errs.KindOf(err), errs.KindInternal)
	}
}

func TestNewPoolConfigDoesNotLeakThePassword(t *testing.T) {
	t.Parallel()

	const password = "s3cr3t-quire-password"

	cfg := databaseConfig()
	cfg.URL = config.Secret("postgres://quire:" + password + "@localhost:5432/quire?sslmode=@")

	_, err := persist.NewPoolConfig(cfg)
	if err == nil {
		t.Fatal("NewPoolConfig accepted a malformed URL")
	}

	// The cause is wrapped for the logs, so the driver must be the one
	// redacting the password out of its parse error.
	if strings.Contains(err.Error(), password) {
		t.Errorf("the error names the password: %v", err)
	}
}
