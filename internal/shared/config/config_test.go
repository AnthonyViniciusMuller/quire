package config_test

import (
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
)

// requiredEnv is the smallest environment a node can start with: everything
// else has a default.
func requiredEnv() map[string]string {
	return map[string]string{
		"QUIRE_SERVER_NAME":               "quire-a.example",
		"QUIRE_DATABASE_URL":              "postgres://quire:quire@localhost:5432/quire?sslmode=disable",
		"QUIRE_STORAGE_ENDPOINT":          "http://localhost:9000",
		"QUIRE_STORAGE_BUCKET":            "quire-contents",
		"QUIRE_STORAGE_ACCESS_KEY_ID":     "quire",
		"QUIRE_STORAGE_SECRET_ACCESS_KEY": "quire-secret",
		"QUIRE_AUTH_PRIVATE_KEY_PEM":      "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----",
		"QUIRE_AUTH_KEY_ID":               "2026-08",
	}
}

// envWith returns the minimal environment overridden by extra.
func envWith(t *testing.T, extra map[string]string) map[string]string {
	t.Helper()

	env := requiredEnv()
	maps.Copy(env, extra)

	return env
}

// load decodes env and fails the test if it does not decode.
func load(t *testing.T, env map[string]string) *config.Config {
	t.Helper()

	cfg, err := config.LoadFrom(env)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v, want nil", err)
	}

	return cfg
}

// loadErr decodes env and fails the test if it does decode.
func loadErr(t *testing.T, env map[string]string) string {
	t.Helper()

	cfg, err := config.LoadFrom(env)
	if err == nil {
		t.Fatalf("LoadFrom() = %+v, want an error", cfg)
	}

	return err.Error()
}

func TestLoadFromAppliesDefaults(t *testing.T) {
	t.Parallel()

	cfg := load(t, requiredEnv())

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"environment", cfg.Environment, config.Development},
		{"grpc address", cfg.Server.GRPCAddress, ":9090"},
		{"http address", cfg.Server.HTTPAddress, ":8080"},
		{"shutdown timeout", cfg.Server.ShutdownTimeout, 15 * time.Second},
		{"max connections", cfg.Database.MaxConnections, 10},
		{"min connections", cfg.Database.MinConnections, 2},
		{"storage region", cfg.Storage.Region, "us-east-1"},
		{"storage path style", cfg.Storage.UsePathStyle, true},
		{"access token ttl", cfg.Auth.AccessTokenTTL, 15 * time.Minute},
		{"refresh token ttl", cfg.Auth.RefreshTokenTTL, 30 * 24 * time.Hour},
		{"bcrypt cost", cfg.Auth.BcryptCost, 12},
		{"replication batch size", cfg.Federation.ReplicationBatchSize, 500},
		{"insecure discovery", cfg.Federation.AllowInsecureDiscovery, false},
		{"log level", cfg.Log.Level, slog.LevelInfo},
		{"log format", cfg.Log.Format, config.LogFormatJSON},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestLoadFromDerivesValuesFromTheServerName(t *testing.T) {
	t.Parallel()

	cfg := load(t, requiredEnv())

	if got, want := cfg.Server.BaseURL.String(), "https://quire-a.example"; got != want {
		t.Errorf("BaseURL = %q, want %q", got, want)
	}

	if got, want := cfg.Auth.Issuer, "https://quire-a.example"; got != want {
		t.Errorf("Issuer = %q, want %q", got, want)
	}

	if got, want := cfg.Server.GRPCAdvertisedAddress, "quire-a.example:9090"; got != want {
		t.Errorf("GRPCAdvertisedAddress = %q, want %q", got, want)
	}
}

func TestLoadFromKeepsExplicitValuesOverDerivedOnes(t *testing.T) {
	t.Parallel()

	cfg := load(t, envWith(t, map[string]string{
		"QUIRE_SERVER_BASE_URL":         "https://reader.example/quire/",
		"QUIRE_AUTH_ISSUER":             "https://issuer.example",
		"QUIRE_GRPC_ADVERTISED_ADDRESS": "10.0.0.7:9443",
	}))

	// The trailing slash is trimmed so that concatenating a .well-known path
	// never produces a double slash.
	if got, want := cfg.Server.BaseURL.String(), "https://reader.example/quire"; got != want {
		t.Errorf("BaseURL = %q, want %q", got, want)
	}

	if got, want := cfg.Auth.Issuer, "https://issuer.example"; got != want {
		t.Errorf("Issuer = %q, want %q", got, want)
	}

	if got, want := cfg.Server.GRPCAdvertisedAddress, "10.0.0.7:9443"; got != want {
		t.Errorf("GRPCAdvertisedAddress = %q, want %q", got, want)
	}
}

func TestLoadFromTreatsBlankValuesAsUnset(t *testing.T) {
	t.Parallel()

	// Container tooling routinely injects empty variables; they must fall back
	// to the default rather than override it with an empty string.
	cfg := load(t, envWith(t, map[string]string{
		"QUIRE_GRPC_ADDRESS":     "  ",
		"QUIRE_STORAGE_REGION":   "",
		"QUIRE_AUTH_BCRYPT_COST": "",
	}))

	if got, want := cfg.Server.GRPCAddress, ":9090"; got != want {
		t.Errorf("GRPCAddress = %q, want %q", got, want)
	}

	if got, want := cfg.Storage.Region, "us-east-1"; got != want {
		t.Errorf("Region = %q, want %q", got, want)
	}

	if got, want := cfg.Auth.BcryptCost, 12; got != want {
		t.Errorf("BcryptCost = %d, want %d", got, want)
	}
}

func TestLoadFromTrimsSurroundingSpace(t *testing.T) {
	t.Parallel()

	cfg := load(t, envWith(t, map[string]string{"QUIRE_SERVER_NAME": "  quire-b.example  "}))

	if got, want := cfg.Server.Name, "quire-b.example"; got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}
}

func TestLoadFromReportsEveryMissingRequiredVariable(t *testing.T) {
	t.Parallel()

	// One startup attempt must name every problem, not just the first.
	message := loadErr(t, map[string]string{})

	for _, key := range []string{
		"QUIRE_SERVER_NAME",
		"QUIRE_DATABASE_URL",
		"QUIRE_STORAGE_ENDPOINT",
		"QUIRE_STORAGE_BUCKET",
		"QUIRE_STORAGE_ACCESS_KEY_ID",
		"QUIRE_STORAGE_SECRET_ACCESS_KEY",
		"QUIRE_AUTH_PRIVATE_KEY_PEM",
		"QUIRE_AUTH_KEY_ID",
	} {
		if !strings.Contains(message, key) {
			t.Errorf("error does not mention %s:\n%s", key, message)
		}
	}
}

func TestLoadFromRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		env  map[string]string
		want string
	}{
		// The decoder names the offending value, which is what a startup log
		// needs in order to point at the wrong variable.
		"integer":   {map[string]string{"QUIRE_DATABASE_MAX_CONNECTIONS": "many"}, `"many"`},
		"boolean":   {map[string]string{"QUIRE_STORAGE_USE_PATH_STYLE": "perhaps"}, `"perhaps"`},
		"duration":  {map[string]string{"QUIRE_SHUTDOWN_TIMEOUT": "soon"}, `"soon"`},
		"log level": {map[string]string{"QUIRE_LOG_LEVEL": "chatty"}, `"chatty"`},
		"url":       {map[string]string{"QUIRE_SERVER_BASE_URL": "://nope"}, `"://nope"`},
		// Closed sets are checked by Validate, which names the variable.
		"log format": {
			map[string]string{"QUIRE_LOG_FORMAT": "xml"},
			`QUIRE_LOG_FORMAT: "xml" must be one of json, text`,
		},
		"profile": {
			map[string]string{"QUIRE_ENV": "staging"},
			`QUIRE_ENV: "staging" must be one of development, production`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if message := loadErr(t, envWith(t, tt.env)); !strings.Contains(message, tt.want) {
				t.Errorf("error = %q, want it to contain %q", message, tt.want)
			}
		})
	}
}

func TestValidateRejectsInconsistentValues(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		env  map[string]string
		want string
	}{
		"server name carrying a scheme": {
			map[string]string{"QUIRE_SERVER_NAME": "https://quire-a.example"},
			"must be a bare host",
		},
		"more minimum than maximum connections": {
			map[string]string{
				"QUIRE_DATABASE_MIN_CONNECTIONS": "20",
				"QUIRE_DATABASE_MAX_CONNECTIONS": "5",
			},
			"exceeds the maximum",
		},
		"access token outliving the refresh token": {
			map[string]string{
				"QUIRE_AUTH_ACCESS_TOKEN_TTL":  "48h",
				"QUIRE_AUTH_REFRESH_TOKEN_TTL": "1h",
			},
			"must be shorter than the refresh token",
		},
		"bcrypt cost out of range": {
			map[string]string{"QUIRE_AUTH_BCRYPT_COST": "2"},
			"outside the range",
		},
		"half a tls keypair": {
			map[string]string{"QUIRE_FEDERATION_TLS_CERT_FILE": "/etc/quire/tls.crt"},
			"set both or neither",
		},
		"plain http in production": {
			map[string]string{
				"QUIRE_ENV":             "production",
				"QUIRE_SERVER_BASE_URL": "http://quire-a.example",
			},
			"not allowed in production",
		},
		"insecure discovery in production": {
			map[string]string{
				"QUIRE_ENV": "production",
				"QUIRE_FEDERATION_ALLOW_INSECURE_DISCOVERY": "true",
			},
			"not allowed in production",
		},
		"non-positive shutdown timeout": {
			map[string]string{"QUIRE_SHUTDOWN_TIMEOUT": "0s"},
			"must be positive",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if message := loadErr(t, envWith(t, tt.env)); !strings.Contains(message, tt.want) {
				t.Errorf("error = %q, want it to contain %q", message, tt.want)
			}
		})
	}
}

func TestSecretNeverRendersItsValue(t *testing.T) {
	t.Parallel()

	const value = "hunter2"
	secret := config.Secret(value)

	t.Run("carried inside a formatted struct", func(t *testing.T) {
		t.Parallel()

		// The realistic accident: dumping the whole configuration.
		cfg := load(t, envWith(t, map[string]string{
			"QUIRE_DATABASE_URL":              "postgres://user:" + value + "@db/quire",
			"QUIRE_STORAGE_SECRET_ACCESS_KEY": value,
			"QUIRE_AUTH_PRIVATE_KEY_PEM":      value,
		}))

		if rendered := fmt.Sprintf("%+v", cfg); strings.Contains(rendered, value) {
			t.Errorf("rendered configuration leaks a secret:\n%s", rendered)
		}
	})

	t.Run("logged", func(t *testing.T) {
		t.Parallel()

		if logged := secret.LogValue().String(); strings.Contains(logged, value) {
			t.Errorf("LogValue() = %q, want the value to be hidden", logged)
		}
	})

	t.Run("marshalled as text", func(t *testing.T) {
		t.Parallel()

		text, err := secret.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText() error = %v, want nil", err)
		}

		if strings.Contains(string(text), value) {
			t.Errorf("MarshalText() = %q, want the value to be hidden", text)
		}
	})

	t.Run("revealed on purpose", func(t *testing.T) {
		t.Parallel()

		if got := secret.Reveal(); got != value {
			t.Errorf("Reveal() = %q, want %q", got, value)
		}
	})
}

func TestSecretIsZero(t *testing.T) {
	t.Parallel()

	if !config.Secret("").IsZero() {
		t.Error("empty secret reports as set")
	}

	if config.Secret("x").IsZero() {
		t.Error("non-empty secret reports as unset")
	}
}
