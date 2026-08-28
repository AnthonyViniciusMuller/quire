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
		"QUIRE_SERVER_NAME":                     "quire-a.example",
		"QUIRE_DATABASE_URL":                    "postgres://quire:quire@localhost:5432/quire?sslmode=disable",
		"QUIRE_STORAGE_BUCKET":                  "quire-contents",
		"QUIRE_STORAGE_MINIO_ENDPOINT":          "localhost:9000",
		"QUIRE_STORAGE_MINIO_ACCESS_KEY_ID":     "quire",
		"QUIRE_STORAGE_MINIO_SECRET_ACCESS_KEY": "quire-secret",
		"QUIRE_AUTH_PRIVATE_KEY_PEM":            "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----",
		"QUIRE_AUTH_KEY_ID":                     "2026-08",
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
		{"storage provider", cfg.Storage.Provider(), config.StorageProviderMinIO},
		{"minio tls", cfg.Storage.MinIO.UseTLS, false},
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

// The advertised address is what every peer in the federation dials, and it
// is published in the .well-known document, so a derivation that mangles the
// port is not a cosmetic defect.
func TestLoadFromDerivesTheAdvertisedAddressFromAnyListenAddress(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"the default listen address":          ":9090",
		"bound to one interface":              "0.0.0.0:9090",
		"bound to every interface, over ipv6": "[::]:9090",
		"bound to the ipv6 loopback":          "[::1]:9090",
	}

	for name, listen := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := load(t, envWith(t, map[string]string{"QUIRE_GRPC_ADDRESS": listen}))

			if got, want := cfg.Server.GRPCAdvertisedAddress, "quire-a.example:9090"; got != want {
				t.Errorf("GRPCAdvertisedAddress = %q, want %q", got, want)
			}
		})
	}
}

// The object store is chosen by which section the deployment filled in rather
// than by a provider variable, so that a name and the credentials beside it
// cannot disagree.
func TestStorageProviderIsTheSectionThatWasFilledIn(t *testing.T) {
	t.Parallel()

	bare := map[string]string{
		"QUIRE_SERVER_NAME":          "quire-a.example",
		"QUIRE_DATABASE_URL":         "postgres://quire:quire@localhost:5432/quire?sslmode=disable",
		"QUIRE_STORAGE_BUCKET":       "quire-contents",
		"QUIRE_AUTH_PRIVATE_KEY_PEM": "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----",
		"QUIRE_AUTH_KEY_ID":          "2026-08",
	}

	tests := map[string]struct {
		section map[string]string
		want    config.StorageProvider
	}{
		"minio": {map[string]string{
			"QUIRE_STORAGE_MINIO_ENDPOINT":          "localhost:9000",
			"QUIRE_STORAGE_MINIO_ACCESS_KEY_ID":     "quire",
			"QUIRE_STORAGE_MINIO_SECRET_ACCESS_KEY": "quire-secret",
		}, config.StorageProviderMinIO},
		"s3": {map[string]string{
			"QUIRE_STORAGE_S3_REGION":            "sa-east-1",
			"QUIRE_STORAGE_S3_ACCESS_KEY_ID":     "AKIA",
			"QUIRE_STORAGE_S3_SECRET_ACCESS_KEY": "secret",
		}, config.StorageProviderS3},
		"gcs": {map[string]string{
			"QUIRE_STORAGE_GCS_PROJECT_ID": "quire-tcc",
		}, config.StorageProviderGCS},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := maps.Clone(bare)
			maps.Copy(env, testCase.section)

			if got := load(t, env).Storage.Provider(); got != testCase.want {
				t.Errorf("Provider() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// A deployment that named two stores has not said which of them holds the
// objects the rows already point at, and picking one would be picking which
// half of the library still opens.
func TestValidateRefusesMoreThanOneObjectStore(t *testing.T) {
	t.Parallel()

	reported := loadErr(t, envWith(t, map[string]string{"QUIRE_STORAGE_GCS_PROJECT_ID": "quire-tcc"}))

	for _, want := range []string{"minio", "gcs"} {
		if !strings.Contains(reported, want) {
			t.Errorf("LoadFrom() error = %q, which does not name %s", reported, want)
		}
	}
}

func TestValidateRefusesANodeWithNowhereToPutAFile(t *testing.T) {
	t.Parallel()

	env := envWith(t, nil)
	for key := range env {
		if strings.HasPrefix(key, "QUIRE_STORAGE_MINIO_") {
			delete(env, key)
		}
	}

	reported := loadErr(t, env)
	if !strings.Contains(reported, "no object store was configured") {
		t.Errorf("LoadFrom() error = %q, which does not say that no store was configured", reported)
	}
}

// MinIO has no credential chain to fall back on, so half a section is a
// deployment that meant to configure it and got it wrong — which is worth
// being told, rather than being told that no store was named.
func TestValidateRefusesAnIncompleteMinIOSection(t *testing.T) {
	t.Parallel()

	env := envWith(t, nil)
	delete(env, "QUIRE_STORAGE_MINIO_SECRET_ACCESS_KEY")

	reported := loadErr(t, env)
	if !strings.Contains(reported, "QUIRE_STORAGE_MINIO_SECRET_ACCESS_KEY") {
		t.Errorf("LoadFrom() error = %q, which does not name the missing half", reported)
	}
}

// The SDK takes an authority and not a URL, and a value with a scheme in it is
// a mistake that would otherwise surface as a dial failure at the first import.
func TestValidateRefusesAMinIOEndpointWithAScheme(t *testing.T) {
	t.Parallel()

	reported := loadErr(t, envWith(t, map[string]string{
		"QUIRE_STORAGE_MINIO_ENDPOINT": "http://localhost:9000",
	}))

	if !strings.Contains(reported, "without a scheme") {
		t.Errorf("LoadFrom() error = %q", reported)
	}
}

// The SDK's own credential chain lives in modules this node does not depend
// on, so a region without a key pair is a node that would fail at the first
// import rather than at startup.
func TestValidateRefusesAnIncompleteS3Section(t *testing.T) {
	t.Parallel()

	env := envWith(t, map[string]string{"QUIRE_STORAGE_S3_REGION": "sa-east-1"})
	for key := range env {
		if strings.HasPrefix(key, "QUIRE_STORAGE_MINIO_") {
			delete(env, key)
		}
	}

	if reported := loadErr(t, env); !strings.Contains(reported, "QUIRE_STORAGE_S3_ACCESS_KEY_ID") {
		t.Errorf("LoadFrom() error = %q, which does not name the missing credentials", reported)
	}

	env["QUIRE_STORAGE_S3_ACCESS_KEY_ID"] = "AKIA"
	env["QUIRE_STORAGE_S3_SECRET_ACCESS_KEY"] = "secret"

	if got := load(t, env).Storage.Provider(); got != config.StorageProviderS3 {
		t.Errorf("Provider() = %q, want s3", got)
	}
}

// Outside development the listen port and the port peers dial are routinely
// different — a node behind a gateway listens on 9090 and is reached on 443 —
// so a derived value would publish an address nobody can connect to.
func TestValidateRequiresTheAdvertisedAddressOutsideDevelopment(t *testing.T) {
	t.Parallel()

	reported := loadErr(t, envWith(t, map[string]string{"QUIRE_ENV": "production"}))

	if !strings.Contains(reported, "QUIRE_GRPC_ADVERTISED_ADDRESS") {
		t.Errorf("LoadFrom() error = %q, which does not name the variable at fault", reported)
	}
}

func TestValidateAcceptsAnAdvertisedAddressGivenExplicitly(t *testing.T) {
	t.Parallel()

	cfg := load(t, envWith(t, map[string]string{
		"QUIRE_ENV":                     "production",
		"QUIRE_GRPC_ADVERTISED_ADDRESS": "quire-a.example:443",
		"QUIRE_STORAGE_MINIO_USE_TLS":   "true",
	}))

	if got, want := cfg.Server.GRPCAdvertisedAddress, "quire-a.example:443"; got != want {
		t.Errorf("GRPCAdvertisedAddress = %q, want %q", got, want)
	}
}

func TestValidateRejectsAnAdvertisedAddressWithoutAPort(t *testing.T) {
	t.Parallel()

	reported := loadErr(t, envWith(t, map[string]string{
		"QUIRE_GRPC_ADVERTISED_ADDRESS": "quire-a.example",
	}))

	if !strings.Contains(reported, "QUIRE_GRPC_ADVERTISED_ADDRESS") {
		t.Errorf("LoadFrom() error = %q, which does not name the variable at fault", reported)
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
		"QUIRE_GRPC_ADDRESS":             "  ",
		"QUIRE_DATABASE_MAX_CONNECTIONS": "",
		"QUIRE_AUTH_BCRYPT_COST":         "",
	}))

	if got, want := cfg.Server.GRPCAddress, ":9090"; got != want {
		t.Errorf("GRPCAddress = %q, want %q", got, want)
	}

	if got, want := cfg.Database.MaxConnections, 10; got != want {
		t.Errorf("MaxConnections = %d, want %d", got, want)
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
		"QUIRE_STORAGE_BUCKET",
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
		"boolean":   {map[string]string{"QUIRE_STORAGE_MINIO_USE_TLS": "perhaps"}, `"perhaps"`},
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
			"QUIRE_DATABASE_URL":                    "postgres://user:" + value + "@db/quire",
			"QUIRE_STORAGE_MINIO_SECRET_ACCESS_KEY": value,
			"QUIRE_AUTH_PRIVATE_KEY_PEM":            value,
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

// smtpEnv is the smallest mail section that decodes: a relay and a sender.
func smtpEnv() map[string]string {
	return map[string]string{
		"QUIRE_MAIL_SMTP_HOST":    "relay.quire-a.example",
		"QUIRE_MAIL_FROM_ADDRESS": "no-reply@quire-a.example",
	}
}

func TestMailTransportIsInferredFromTheSectionFilledIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want config.MailTransport
	}{
		{"nothing configured", nil, config.MailTransportNone},
		{"the relay", smtpEnv(), config.MailTransportSMTP},
		{
			// Half a section is a deployment that chose SMTP and got it wrong,
			// which is more useful to be told than that it chose nothing.
			name: "credentials without a relay",
			env: map[string]string{
				"QUIRE_MAIL_SMTP_USERNAME": "quire",
				"QUIRE_MAIL_SMTP_PASSWORD": "secret",
			},
			want: config.MailTransportSMTP,
		},
		{
			// The sender is shared by every transport, as the bucket is by
			// every object store, so it selects nothing on its own.
			name: "a sender alone",
			env:  map[string]string{"QUIRE_MAIL_FROM_ADDRESS": "no-reply@quire-a.example"},
			want: config.MailTransportNone,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// The half-configured cases do not load, so the section is read off
			// a value the decoder filled in rather than off a whole config.
			cfg, err := config.LoadFrom(envWith(t, test.env))
			if err != nil && test.want == config.MailTransportNone {
				t.Fatalf("LoadFrom() error = %v, want nil", err)
			}

			if cfg == nil {
				cfg = &config.Config{Mail: mailFrom(test.env)}
			}

			if got := cfg.Mail.Transport(); got != test.want {
				t.Errorf("Transport() = %q, want %q", got, test.want)
			}
		})
	}
}

// mailFrom builds the section by hand, for the environments that do not load.
func mailFrom(env map[string]string) config.Mail {
	return config.Mail{
		FromAddress: env["QUIRE_MAIL_FROM_ADDRESS"],
		SMTP: config.MailSMTP{
			Host:     env["QUIRE_MAIL_SMTP_HOST"],
			Username: env["QUIRE_MAIL_SMTP_USERNAME"],
			Password: config.Secret(env["QUIRE_MAIL_SMTP_PASSWORD"]),
		},
	}
}

func TestLoadFromAppliesMailDefaults(t *testing.T) {
	t.Parallel()

	cfg := load(t, envWith(t, smtpEnv()))

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"port", cfg.Mail.SMTP.Port, 587},
		{"security", cfg.Mail.SMTP.Security, config.MailSecurityStartTLS},
		{"from name", cfg.Mail.FromName, "Quire"},
		{"delivery timeout", cfg.Mail.DeliveryTimeout, 30 * time.Second},
		{"queue size", cfg.Mail.QueueSize, 256},
	}

	for _, test := range tests {
		if test.got != test.want {
			t.Errorf("%s = %v, want %v", test.name, test.got, test.want)
		}
	}
}

func TestLoadFromRejectsAnUnusableMailSection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "no sender",
			env:  map[string]string{"QUIRE_MAIL_SMTP_HOST": "relay.quire-a.example"},
			want: "QUIRE_MAIL_FROM_ADDRESS",
		},
		{
			name: "a sender that is not an address",
			env: map[string]string{
				"QUIRE_MAIL_SMTP_HOST":    "relay.quire-a.example",
				"QUIRE_MAIL_FROM_ADDRESS": "quire-a.example",
			},
			want: "QUIRE_MAIL_FROM_ADDRESS",
		},
		{
			name: "credentials without a relay",
			env: map[string]string{
				"QUIRE_MAIL_FROM_ADDRESS":  "no-reply@quire-a.example",
				"QUIRE_MAIL_SMTP_USERNAME": "quire",
				"QUIRE_MAIL_SMTP_PASSWORD": "secret",
			},
			want: "QUIRE_MAIL_SMTP_HOST",
		},
		{
			name: "a port written into the host",
			env: map[string]string{
				"QUIRE_MAIL_FROM_ADDRESS": "no-reply@quire-a.example",
				"QUIRE_MAIL_SMTP_HOST":    "relay.quire-a.example:587",
			},
			want: "QUIRE_MAIL_SMTP_HOST",
		},
		{
			name: "a port outside the range",
			env: map[string]string{
				"QUIRE_MAIL_FROM_ADDRESS": "no-reply@quire-a.example",
				"QUIRE_MAIL_SMTP_HOST":    "relay.quire-a.example",
				"QUIRE_MAIL_SMTP_PORT":    "70000",
			},
			want: "QUIRE_MAIL_SMTP_PORT",
		},
		{
			name: "half a credential",
			env: map[string]string{
				"QUIRE_MAIL_FROM_ADDRESS":  "no-reply@quire-a.example",
				"QUIRE_MAIL_SMTP_HOST":     "relay.quire-a.example",
				"QUIRE_MAIL_SMTP_USERNAME": "quire",
			},
			want: "QUIRE_MAIL_SMTP_PASSWORD",
		},
		{
			name: "an unknown way of protecting the connection",
			env: map[string]string{
				"QUIRE_MAIL_FROM_ADDRESS":  "no-reply@quire-a.example",
				"QUIRE_MAIL_SMTP_HOST":     "relay.quire-a.example",
				"QUIRE_MAIL_SMTP_SECURITY": "ssl",
			},
			want: "QUIRE_MAIL_SMTP_SECURITY",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := loadErr(t, envWith(t, test.env)); !strings.Contains(got, test.want) {
				t.Errorf("LoadFrom() error = %q, want it to name %s", got, test.want)
			}
		})
	}
}

// A recovery credential is what replaces a password, so the connection that
// carries it is held to the standard the password itself is — and the check is
// on the profile, because the relay a developer runs beside the node speaks no
// TLS at all.
func TestProductionRefusesToSubmitARecoveryInTheClear(t *testing.T) {
	t.Parallel()

	env := envWith(t, smtpEnv())
	env["QUIRE_ENV"] = string(config.Production)
	env["QUIRE_GRPC_ADVERTISED_ADDRESS"] = "quire-a.example:443"
	env["QUIRE_STORAGE_MINIO_USE_TLS"] = "true"
	env["QUIRE_MAIL_SMTP_SECURITY"] = string(config.MailSecurityNone)

	if got := loadErr(t, env); !strings.Contains(got, "QUIRE_MAIL_SMTP_SECURITY") {
		t.Errorf("LoadFrom() error = %q, want it to name QUIRE_MAIL_SMTP_SECURITY", got)
	}

	// The same deployment with the connection protected is a deployment that
	// loads, which is what makes the check above about the clear text and not
	// about the profile.
	env["QUIRE_MAIL_SMTP_SECURITY"] = string(config.MailSecurityStartTLS)

	if cfg := load(t, env); cfg.Mail.Transport() != config.MailTransportSMTP {
		t.Errorf("Transport() = %q, want %q", cfg.Mail.Transport(), config.MailTransportSMTP)
	}
}
