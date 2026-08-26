// Package config loads the whole node configuration from the process
// environment into a single immutable struct.
//
// Nothing outside this package reads an environment variable: every component
// receives the section it needs from the container in internal/<slice>/di. That
// keeps the configuration surface enumerable, testable without touching the
// real environment, and impossible to extend by accident from a leaf package.
//
// Decoding is delegated to github.com/caarlos0/env, which reports every
// misconfigured variable at once instead of stopping at the first one. What the
// tags cannot express stays in Go: values derived from other values live in
// [Config.resolveDerived], and rules spanning more than one field live in
// [Config.Validate].
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Environment names the deployment profile of the node.
type Environment string

// The recognized deployment profiles.
const (
	// Development relaxes the checks that only make sense with real
	// certificates and real peers, so that the local federation can run over
	// plain HTTP.
	Development Environment = "development"
	// Production demands transport security everywhere.
	Production Environment = "production"
)

// IsProduction reports whether the node runs with production guarantees.
func (e Environment) IsProduction() bool { return e == Production }

// LogFormat names the encoding of the log stream.
type LogFormat string

// The recognized log encodings.
const (
	// LogFormatJSON emits one JSON object per record, for log aggregation.
	LogFormatJSON LogFormat = "json"
	// LogFormatText emits human-readable records, for local development.
	LogFormatText LogFormat = "text"
)

// Config is the complete configuration of a Quire node.
type Config struct {
	// Environment is the deployment profile.
	Environment Environment `env:"QUIRE_ENV" envDefault:"development"`
	// Server identifies the node inside the federation.
	Server Server
	// Database addresses the PostgreSQL instance holding the node state.
	Database Database
	// Storage addresses the object store holding the e-book files.
	Storage Storage
	// Auth configures token issuance and password handling.
	Auth Auth
	// Federation configures discovery and node-to-node replication.
	Federation Federation
	// Log configures the structured logger.
	Log Log
}

// Server describes the identity and the listeners of the node.
type Server struct {
	// Name is the federation domain of the node, the part after the colon in a
	// federated identifier such as @anthony:quire-a.example. It is the
	// authority used to discover the node over RFC 8615.
	Name string `env:"QUIRE_SERVER_NAME,required,notEmpty"`
	// BaseURL is the public origin of the node, where the .well-known
	// documents are served. It defaults to https:// followed by Name.
	BaseURL *url.URL `env:"QUIRE_SERVER_BASE_URL"`
	// GRPCAddress is the listen address of the gRPC API.
	GRPCAddress string `env:"QUIRE_GRPC_ADDRESS" envDefault:":9090"`
	// HTTPAddress is the listen address of the discovery, health and metrics
	// endpoints, which cannot be served over gRPC because RFC 8615 mandates
	// plain HTTP paths.
	HTTPAddress string `env:"QUIRE_HTTP_ADDRESS" envDefault:":8080"`
	// GRPCAdvertisedAddress is the host:port peers should dial for gRPC. It is
	// published in the .well-known document and defaults to Name with the port
	// taken from GRPCAddress.
	GRPCAdvertisedAddress string `env:"QUIRE_GRPC_ADVERTISED_ADDRESS"`
	// ShutdownTimeout bounds the graceful shutdown of both servers.
	ShutdownTimeout time.Duration `env:"QUIRE_SHUTDOWN_TIMEOUT" envDefault:"15s"`
}

// Database describes the PostgreSQL connection pool.
type Database struct {
	// URL is the libpq connection string.
	URL Secret `env:"QUIRE_DATABASE_URL,required,notEmpty"`
	// MaxConnections is the upper bound of the pool.
	MaxConnections int `env:"QUIRE_DATABASE_MAX_CONNECTIONS" envDefault:"10"`
	// MinConnections is the number of connections kept warm.
	MinConnections int `env:"QUIRE_DATABASE_MIN_CONNECTIONS" envDefault:"2"`
	// MaxConnectionLifetime retires a connection after this long, so that the
	// pool follows a rolling database upgrade.
	MaxConnectionLifetime time.Duration `env:"QUIRE_DATABASE_MAX_CONNECTION_LIFETIME" envDefault:"1h"`
	// MaxConnectionIdleTime closes a connection left unused for this long.
	MaxConnectionIdleTime time.Duration `env:"QUIRE_DATABASE_MAX_CONNECTION_IDLE_TIME" envDefault:"30m"`
	// ConnectTimeout bounds the initial connection attempt.
	ConnectTimeout time.Duration `env:"QUIRE_DATABASE_CONNECT_TIMEOUT" envDefault:"10s"`
}

// Storage describes the S3-compatible object store that holds e-book files.
// Only the file bytes live here; every piece of metadata stays in PostgreSQL.
type Storage struct {
	// Endpoint is the base URL of the object store, MinIO in development and
	// S3 or GCS in the cloud.
	Endpoint string `env:"QUIRE_STORAGE_ENDPOINT,required,notEmpty"`
	// Region is the bucket region.
	Region string `env:"QUIRE_STORAGE_REGION" envDefault:"us-east-1"`
	// Bucket holds the e-book contents, keyed by content hash.
	Bucket string `env:"QUIRE_STORAGE_BUCKET,required,notEmpty"`
	// AccessKeyID identifies the credentials.
	AccessKeyID string `env:"QUIRE_STORAGE_ACCESS_KEY_ID,required,notEmpty"`
	// SecretAccessKey authenticates the credentials.
	SecretAccessKey Secret `env:"QUIRE_STORAGE_SECRET_ACCESS_KEY,required,notEmpty"`
	// UsePathStyle addresses buckets as a path segment rather than a subdomain,
	// which MinIO requires.
	UsePathStyle bool `env:"QUIRE_STORAGE_USE_PATH_STYLE" envDefault:"true"`
	// PresignTTL bounds how long a download link stays valid.
	PresignTTL time.Duration `env:"QUIRE_STORAGE_PRESIGN_TTL" envDefault:"15m"`
}

// Auth describes token issuance, verification and password handling.
type Auth struct {
	// PrivateKeyPEM is the PEM-encoded key that signs access tokens. Its public
	// half is published at /.well-known/jwks.json so that peers, and the
	// service mesh, can verify tokens without contacting this node.
	PrivateKeyPEM Secret `env:"QUIRE_AUTH_PRIVATE_KEY_PEM,required,notEmpty"`
	// KeyID is the JWKS key identifier carried in the token header, which is
	// what makes key rotation possible.
	KeyID string `env:"QUIRE_AUTH_KEY_ID,required,notEmpty"`
	// Issuer is the iss claim. It defaults to the node base URL.
	Issuer string `env:"QUIRE_AUTH_ISSUER"`
	// AccessTokenTTL bounds the lifetime of an access token.
	AccessTokenTTL time.Duration `env:"QUIRE_AUTH_ACCESS_TOKEN_TTL" envDefault:"15m"`
	// RefreshTokenTTL bounds the lifetime of a refresh token, and with it how
	// long a device may stay offline before having to authenticate again.
	RefreshTokenTTL time.Duration `env:"QUIRE_AUTH_REFRESH_TOKEN_TTL" envDefault:"720h"`
	// PasswordResetTTL bounds the lifetime of a password reset token.
	PasswordResetTTL time.Duration `env:"QUIRE_AUTH_PASSWORD_RESET_TTL" envDefault:"1h"`
	// BcryptCost is the work factor used to hash passwords.
	BcryptCost int `env:"QUIRE_AUTH_BCRYPT_COST" envDefault:"12"`
}

// Federation describes how the node discovers and talks to its peers.
type Federation struct {
	// DiscoveryTimeout bounds a .well-known lookup against a peer.
	DiscoveryTimeout time.Duration `env:"QUIRE_FEDERATION_DISCOVERY_TIMEOUT" envDefault:"10s"`
	// ReplicationInterval is how often the replication worker drains pending
	// operations towards the nodes a user has authorized.
	ReplicationInterval time.Duration `env:"QUIRE_FEDERATION_REPLICATION_INTERVAL" envDefault:"30s"`
	// ReplicationBatchSize bounds how many operations travel in one request.
	ReplicationBatchSize int `env:"QUIRE_FEDERATION_REPLICATION_BATCH_SIZE" envDefault:"500"`
	// TLSCertFile is the certificate presented to peers on node-to-node calls.
	TLSCertFile string `env:"QUIRE_FEDERATION_TLS_CERT_FILE"`
	// TLSKeyFile is the private key matching TLSCertFile.
	TLSKeyFile string `env:"QUIRE_FEDERATION_TLS_KEY_FILE"`
	// TLSCAFile is the bundle used to verify peer certificates. When it is
	// empty the system trust store is used.
	TLSCAFile string `env:"QUIRE_FEDERATION_TLS_CA_FILE"`
	// AllowInsecureDiscovery permits discovering peers over plain HTTP. It is
	// rejected outside the development profile.
	AllowInsecureDiscovery bool `env:"QUIRE_FEDERATION_ALLOW_INSECURE_DISCOVERY" envDefault:"false"`
}

// Log describes the structured logger.
type Log struct {
	// Level is the minimum severity that reaches the output.
	Level slog.Level `env:"QUIRE_LOG_LEVEL" envDefault:"info"`
	// Format is the encoding of each record.
	Format LogFormat `env:"QUIRE_LOG_FORMAT" envDefault:"json"`
	// AddSource annotates every record with its call site.
	AddSource bool `env:"QUIRE_LOG_ADD_SOURCE" envDefault:"false"`
}

// Load reads the configuration from the process environment.
func Load() (*Config, error) { return LoadFrom(env.ToMap(os.Environ())) }

// LoadFrom reads the configuration out of environ, which lets tests supply a
// map instead of the real process environment.
func LoadFrom(environ map[string]string) (*Config, error) {
	cfg, err := env.ParseAsWithOptions[Config](env.Options{Environment: trimmed(environ)})
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	cfg.resolveDerived()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	return &cfg, nil
}

// trimmed strips the space around every value. Container tooling routinely
// injects padded or blank variables; a variable left blank falls back to its
// default rather than overriding it with an empty string.
func trimmed(environ map[string]string) map[string]string {
	clean := make(map[string]string, len(environ))
	for key, value := range environ {
		clean[key] = strings.TrimSpace(value)
	}

	return clean
}

// resolveDerived fills the fields whose default depends on another field, which
// no struct tag can express.
func (c *Config) resolveDerived() {
	if c.Server.BaseURL == nil && c.Server.Name != "" {
		c.Server.BaseURL = &url.URL{Scheme: "https", Host: c.Server.Name}
	}

	if c.Server.BaseURL != nil {
		// Trimming here keeps every concatenated .well-known path free of a
		// double slash.
		c.Server.BaseURL.Path = strings.TrimSuffix(c.Server.BaseURL.Path, "/")
	}

	if c.Auth.Issuer == "" && c.Server.BaseURL != nil {
		c.Auth.Issuer = c.Server.BaseURL.String()
	}

	if c.Server.GRPCAdvertisedAddress == "" && c.Server.Name != "" {
		if _, port, found := strings.Cut(c.Server.GRPCAddress, ":"); found {
			c.Server.GRPCAdvertisedAddress = c.Server.Name + ":" + port
		}
	}
}

// Validate reports every configured value that cannot work, joined into a
// single error. It covers what a struct tag cannot: closed sets of values, and
// rules that span more than one field.
func (c *Config) Validate() error {
	return errors.Join(
		errors.Join(c.validateEnums()...),
		errors.Join(c.validateServer()...),
		errors.Join(c.validateDatabase()...),
		errors.Join(c.validateAuth()...),
		errors.Join(c.validateFederation()...),
	)
}

func (c *Config) validateEnums() []error {
	var errs []error

	switch c.Environment {
	case Development, Production:
	default:
		errs = append(errs, fmt.Errorf("QUIRE_ENV: %q must be one of %s, %s",
			c.Environment, Development, Production))
	}

	switch c.Log.Format {
	case LogFormatJSON, LogFormatText:
	default:
		errs = append(errs, fmt.Errorf("QUIRE_LOG_FORMAT: %q must be one of %s, %s",
			c.Log.Format, LogFormatJSON, LogFormatText))
	}

	return errs
}

func (c *Config) validateServer() []error {
	var errs []error

	if strings.ContainsAny(c.Server.Name, "/:@ ") {
		errs = append(errs, fmt.Errorf(
			"QUIRE_SERVER_NAME: %q must be a bare host, without scheme, port or path", c.Server.Name))
	}

	switch {
	case c.Server.BaseURL == nil:
	case c.Server.BaseURL.Host == "":
		errs = append(errs, errors.New("QUIRE_SERVER_BASE_URL: missing host"))
	case c.Server.BaseURL.Scheme != "https" && c.Server.BaseURL.Scheme != "http":
		errs = append(errs, fmt.Errorf(
			"QUIRE_SERVER_BASE_URL: scheme %q must be http or https", c.Server.BaseURL.Scheme))
	case c.Server.BaseURL.Scheme == "http" && c.Environment.IsProduction():
		errs = append(errs, errors.New("QUIRE_SERVER_BASE_URL: plain http is not allowed in production"))
	}

	if c.Server.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("QUIRE_SHUTDOWN_TIMEOUT: must be positive"))
	}

	return errs
}

func (c *Config) validateDatabase() []error {
	var errs []error

	if c.Database.MaxConnections < 1 {
		errs = append(errs, errors.New("QUIRE_DATABASE_MAX_CONNECTIONS: must be at least 1"))
	}

	if c.Database.MinConnections < 0 {
		errs = append(errs, errors.New("QUIRE_DATABASE_MIN_CONNECTIONS: must not be negative"))
	}

	if c.Database.MinConnections > c.Database.MaxConnections {
		errs = append(errs, fmt.Errorf("QUIRE_DATABASE_MIN_CONNECTIONS: %d exceeds the maximum of %d",
			c.Database.MinConnections, c.Database.MaxConnections))
	}

	return errs
}

// bcrypt refuses a cost outside this range, and a cost below the default is a
// silent downgrade of every password in the node.
const (
	minBcryptCost = 4
	maxBcryptCost = 31
)

func (c *Config) validateAuth() []error {
	var errs []error

	if c.Auth.AccessTokenTTL <= 0 {
		errs = append(errs, errors.New("QUIRE_AUTH_ACCESS_TOKEN_TTL: must be positive"))
	}

	if c.Auth.RefreshTokenTTL <= 0 {
		errs = append(errs, errors.New("QUIRE_AUTH_REFRESH_TOKEN_TTL: must be positive"))
	}

	if c.Auth.AccessTokenTTL >= c.Auth.RefreshTokenTTL {
		errs = append(errs, fmt.Errorf(
			"QUIRE_AUTH_ACCESS_TOKEN_TTL: %s must be shorter than the refresh token TTL of %s",
			c.Auth.AccessTokenTTL, c.Auth.RefreshTokenTTL))
	}

	if c.Auth.PasswordResetTTL <= 0 {
		errs = append(errs, errors.New("QUIRE_AUTH_PASSWORD_RESET_TTL: must be positive"))
	}

	if c.Auth.BcryptCost < minBcryptCost || c.Auth.BcryptCost > maxBcryptCost {
		errs = append(errs, fmt.Errorf("QUIRE_AUTH_BCRYPT_COST: %d is outside the range %d to %d",
			c.Auth.BcryptCost, minBcryptCost, maxBcryptCost))
	}

	return errs
}

func (c *Config) validateFederation() []error {
	var errs []error

	if c.Federation.DiscoveryTimeout <= 0 {
		errs = append(errs, errors.New("QUIRE_FEDERATION_DISCOVERY_TIMEOUT: must be positive"))
	}

	if c.Federation.ReplicationInterval <= 0 {
		errs = append(errs, errors.New("QUIRE_FEDERATION_REPLICATION_INTERVAL: must be positive"))
	}

	if c.Federation.ReplicationBatchSize < 1 {
		errs = append(errs, errors.New("QUIRE_FEDERATION_REPLICATION_BATCH_SIZE: must be at least 1"))
	}

	// A certificate without its key, or the other way round, cannot be loaded.
	if (c.Federation.TLSCertFile == "") != (c.Federation.TLSKeyFile == "") {
		errs = append(errs, errors.New(
			"QUIRE_FEDERATION_TLS_CERT_FILE and QUIRE_FEDERATION_TLS_KEY_FILE: set both or neither"))
	}

	if c.Federation.AllowInsecureDiscovery && c.Environment.IsProduction() {
		errs = append(errs, errors.New(
			"QUIRE_FEDERATION_ALLOW_INSECURE_DISCOVERY: plain http discovery is not allowed in production"))
	}

	return errs
}
