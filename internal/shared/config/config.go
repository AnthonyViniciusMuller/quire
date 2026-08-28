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
	"net"
	"net/mail"
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
	// Mail configures the transport that delivers to a reader's address.
	Mail Mail
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
	// published in the .well-known document, so it is the address on which the
	// whole federation reaches this node.
	//
	// In development it defaults to Name with the port taken from GRPCAddress,
	// which is where a node behind nothing does answer. Outside development it
	// is required, because there the two are routinely different: a node behind
	// a gateway listens on 9090 and is reached on 443, and a default derived
	// from the listen port would publish an address no peer can connect to —
	// a failure that looks like an unreachable peer rather than like a
	// misconfigured one.
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

// StorageProvider names which object store holds the e-book files.
//
// It is never configured directly. The node infers it from which section of
// [Storage] the deployment filled in, which is what [Storage.Provider]
// answers — one bucket variable and one section, rather than a provider
// variable that can disagree with the credentials beside it.
type StorageProvider string

// The object stores the node can be pointed at.
const (
	// StorageProviderNone is no section filled in.
	StorageProviderNone StorageProvider = ""
	// StorageProviderS3 is Amazon S3.
	StorageProviderS3 StorageProvider = "s3"
	// StorageProviderMinIO is a self-hosted MinIO.
	StorageProviderMinIO StorageProvider = "minio"
	// StorageProviderGCS is Google Cloud Storage.
	StorageProviderGCS StorageProvider = "gcs"
)

// Storage describes the object store that holds e-book files. Only the file
// bytes live there; every piece of metadata stays in PostgreSQL.
//
// There are three sections and exactly one of them is filled in. The node
// refuses to start on none — it would have nowhere to put a file — and on more
// than one, because a deployment that named two stores has not said which of
// them holds the objects the rows already point at.
type Storage struct {
	// Bucket holds the e-book contents, keyed by content hash. Every provider
	// needs one and it is the same value for all of them, so it is here rather
	// than in each section.
	Bucket string `env:"QUIRE_STORAGE_BUCKET,required,notEmpty"`

	// MaxUploadBytes is the largest file the node will accept.
	//
	// It is checked against the length the client declares before any of the
	// bytes travel, which is what the contract's "description first, content
	// after" shape exists for: a node that discovered the size by receiving it
	// would have received it.
	//
	// The default is half a gigabyte, which is larger than any e-book and
	// smaller than a disk.
	MaxUploadBytes int64 `env:"QUIRE_STORAGE_MAX_UPLOAD_BYTES" envDefault:"536870912"`

	// S3 is Amazon S3, through the AWS SDK.
	S3 StorageS3
	// MinIO is a self-hosted MinIO, through the MinIO SDK.
	MinIO StorageMinIO
	// GCS is Google Cloud Storage, through the Cloud Storage SDK.
	GCS StorageGCS
}

// StorageS3 addresses Amazon S3.
type StorageS3 struct {
	// Region is the bucket region, and the variable that selects this section:
	// an S3 client cannot be built without one, and nothing else here is
	// mandatory.
	Region string `env:"QUIRE_STORAGE_S3_REGION"`
	// AccessKeyID identifies the credentials. Both halves are required: the
	// SDK's own credential chain — an instance role, a service account with
	// IRSA — lives in a module this node does not depend on, and adding it for
	// a deployment that does not exist yet is seven modules for nothing.
	AccessKeyID string `env:"QUIRE_STORAGE_S3_ACCESS_KEY_ID"`
	// SecretAccessKey authenticates the credentials.
	SecretAccessKey Secret `env:"QUIRE_STORAGE_S3_SECRET_ACCESS_KEY"`
	// Endpoint overrides the one the SDK derives from the region, for the
	// S3-compatible services that are neither Amazon nor MinIO.
	Endpoint string `env:"QUIRE_STORAGE_S3_ENDPOINT"`
}

// StorageMinIO addresses a self-hosted MinIO.
type StorageMinIO struct {
	// Endpoint is the host and port MinIO answers on, without a scheme, and
	// the variable that selects this section.
	Endpoint string `env:"QUIRE_STORAGE_MINIO_ENDPOINT"`
	// AccessKeyID identifies the credentials. MinIO has no credential chain to
	// fall back on, so both halves are required once this section is chosen.
	AccessKeyID string `env:"QUIRE_STORAGE_MINIO_ACCESS_KEY_ID"`
	// SecretAccessKey authenticates the credentials.
	SecretAccessKey Secret `env:"QUIRE_STORAGE_MINIO_SECRET_ACCESS_KEY"`
	// UseTLS dials the endpoint over HTTPS. It is false by default because the
	// MinIO a developer runs beside the node is plain HTTP, and it is checked
	// in production for the same reason the discovery client is.
	UseTLS bool `env:"QUIRE_STORAGE_MINIO_USE_TLS" envDefault:"false"`
}

// StorageGCS addresses Google Cloud Storage.
//
// It is the one section whose credentials may be left out, and not because it
// is special: application default credentials are built into the SDK this node
// already depends on, so supporting them costs nothing — which is exactly what
// the equivalent for S3 does not.
type StorageGCS struct {
	// ProjectID is the project the bucket belongs to, and one of the two
	// variables that select this section.
	ProjectID string `env:"QUIRE_STORAGE_GCS_PROJECT_ID"`
	// CredentialsFile is the service account key. Leaving it empty hands the
	// SDK its application default credentials, which is what Workload Identity
	// supplies on GKE and what a developer gets from `gcloud auth`.
	CredentialsFile string `env:"QUIRE_STORAGE_GCS_CREDENTIALS_FILE"`
}

// Provider reports which section the deployment filled in, and
// StorageProviderNone when it filled in none.
//
// A section counts as filled in when any of its variables is set. That is
// deliberately generous: a deployment that set the MinIO endpoint and forgot
// its keys has chosen MinIO and got it wrong, and being told that is more
// useful than being told that no store was configured.
func (s *Storage) Provider() StorageProvider {
	switch {
	case s.MinIO.Endpoint != "" || s.MinIO.AccessKeyID != "" || s.MinIO.SecretAccessKey != "":
		return StorageProviderMinIO
	case s.GCS.ProjectID != "" || s.GCS.CredentialsFile != "":
		return StorageProviderGCS
	case s.S3.Region != "" || s.S3.AccessKeyID != "" || s.S3.SecretAccessKey != "" || s.S3.Endpoint != "":
		return StorageProviderS3
	default:
		return StorageProviderNone
	}
}

// providersConfigured is how many sections the deployment filled in, which is
// what makes "exactly one" checkable.
func (s *Storage) providersConfigured() []StorageProvider {
	var chosen []StorageProvider

	if s.MinIO.Endpoint != "" || s.MinIO.AccessKeyID != "" || s.MinIO.SecretAccessKey != "" {
		chosen = append(chosen, StorageProviderMinIO)
	}

	if s.GCS.ProjectID != "" || s.GCS.CredentialsFile != "" {
		chosen = append(chosen, StorageProviderGCS)
	}

	if s.S3.Region != "" || s.S3.AccessKeyID != "" || s.S3.SecretAccessKey != "" || s.S3.Endpoint != "" {
		chosen = append(chosen, StorageProviderS3)
	}

	return chosen
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

// MailTransport names which outbound transport delivers to a reader's address.
//
// Like [StorageProvider] it is never configured directly. The node infers it
// from which section of [Mail] the deployment filled in, which is what
// [Mail.Transport] answers — a section that is filled in cannot disagree with
// itself, and a variable naming the transport can disagree with the
// credentials beside it.
type MailTransport string

// The transports the node can deliver through.
const (
	// MailTransportNone is no section filled in, and therefore a node that
	// cannot deliver a password recovery at all. C13 in
	// docs/tcc-corrections.md is what that costs, and the adapter in
	// internal/identity/infra/service/mailer is what refuses to ship it
	// outside development.
	MailTransportNone MailTransport = ""
	// MailTransportSMTP is a relay the node submits to over SMTP.
	MailTransportSMTP MailTransport = "smtp"
)

// MailSecurity names how the connection to the relay is protected.
type MailSecurity string

// The recognized ways of protecting a submission.
const (
	// MailSecurityStartTLS connects in the clear and upgrades with STARTTLS,
	// which is what a submission port (587) expects.
	MailSecurityStartTLS MailSecurity = "starttls"
	// MailSecurityTLS dials the relay over TLS from the first byte, which is
	// what an implicit-TLS port (465) expects.
	MailSecurityTLS MailSecurity = "tls"
	// MailSecurityNone submits in the clear. It exists for the relay a
	// developer runs beside the node, and is refused in production for the
	// same reason plain HTTP to the object store is: a recovery credential
	// crosses this connection, and it is the one credential that replaces a
	// password.
	MailSecurityNone MailSecurity = "none"
)

// Mail describes how the node delivers a password recovery to the address on
// record (RF09, UC08).
//
// The address is the only channel a reader who has lost their password still
// has, so a node with no transport here cannot serve the first half of UC08 at
// all. C13 in docs/tcc-corrections.md is that finding; this section is the
// configuration it says the architecture is missing.
type Mail struct {
	// FromAddress is the envelope sender and the From header. Every transport
	// needs one and it is the same value for all of them, so it is here rather
	// than in each section — as [Storage.Bucket] is.
	FromAddress string `env:"QUIRE_MAIL_FROM_ADDRESS"`
	// FromName is the display name beside it.
	FromName string `env:"QUIRE_MAIL_FROM_NAME" envDefault:"Quire"`
	// DeliveryTimeout bounds one delivery attempt. It is not the caller's
	// budget: the delivery is handed to a worker and the call that asked for it
	// has already been answered, so this bounds how long the worker waits on a
	// relay that has stopped talking.
	DeliveryTimeout time.Duration `env:"QUIRE_MAIL_DELIVERY_TIMEOUT" envDefault:"30s"`
	// QueueSize bounds how many deliveries may be waiting at once. A full
	// queue drops the oldest request rather than blocking the call, because a
	// call that blocked on the queue would take longer for an address that
	// exists — which is the channel the queue was introduced to close.
	QueueSize int `env:"QUIRE_MAIL_QUEUE_SIZE" envDefault:"256"`

	// SMTP is a relay the node submits to.
	SMTP MailSMTP
}

// MailSMTP addresses an SMTP relay.
type MailSMTP struct {
	// Host is the relay, and the variable that selects this section.
	Host string `env:"QUIRE_MAIL_SMTP_HOST"`
	// Port is where it answers. The default is the submission port, which is
	// the one a relay expects a program to use.
	Port int `env:"QUIRE_MAIL_SMTP_PORT" envDefault:"587"`
	// Username identifies the credentials, and empty means the relay accepts
	// this node without any — which is what an in-cluster relay reached over
	// the mesh does.
	Username string `env:"QUIRE_MAIL_SMTP_USERNAME"`
	// Password authenticates them.
	Password Secret `env:"QUIRE_MAIL_SMTP_PASSWORD"`
	// Security is how the connection is protected.
	Security MailSecurity `env:"QUIRE_MAIL_SMTP_SECURITY" envDefault:"starttls"`
}

// Transport reports which section the deployment filled in, and
// MailTransportNone when it filled in none.
//
// A section counts as filled in when any of the variables that address the
// relay is set, on the same reasoning as [Storage.Provider]: a deployment that
// set the credentials and forgot the host has chosen SMTP and got it wrong,
// and being told that is more useful than being told it configured no
// transport.
func (m *Mail) Transport() MailTransport {
	if m.SMTP.Host != "" || m.SMTP.Username != "" || !m.SMTP.Password.IsZero() {
		return MailTransportSMTP
	}

	return MailTransportNone
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

	if c.Server.GRPCAdvertisedAddress == "" && c.Server.Name != "" && !c.Environment.IsProduction() {
		// net.SplitHostPort and not a cut at the first colon: a listen address
		// of [::]:9090 has four of them, and cutting at the first one would
		// publish quire-a.example::]:9090 to every peer in the federation.
		if _, port, err := net.SplitHostPort(c.Server.GRPCAddress); err == nil {
			c.Server.GRPCAdvertisedAddress = net.JoinHostPort(c.Server.Name, port)
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
		errors.Join(c.validateStorage()...),
		errors.Join(c.validateMail()...),
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

	switch {
	case c.Server.GRPCAdvertisedAddress == "" && c.Environment.IsProduction():
		errs = append(errs, errors.New(
			"QUIRE_GRPC_ADVERTISED_ADDRESS: required outside development, "+
				"since it is what peers dial and a gateway rarely publishes the listen port"))
	case c.Server.GRPCAdvertisedAddress == "":
	default:
		if _, _, err := net.SplitHostPort(c.Server.GRPCAdvertisedAddress); err != nil {
			errs = append(errs, fmt.Errorf("QUIRE_GRPC_ADVERTISED_ADDRESS: %q must be host:port: %w",
				c.Server.GRPCAdvertisedAddress, err))
		}
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

// validateStorage checks that the deployment named exactly one object store
// and named it completely.
//
// The node refuses to start on none, because it would have nowhere to put a
// file and would only discover that at the first import. It refuses on more
// than one for a less obvious reason: a deployment that named two has not said
// which of them holds the objects the rows in library.ebook_contents already
// point at, and picking one would be picking which half of the library still
// opens.
func (c *Config) validateStorage() []error {
	var errs []error

	if c.Storage.MaxUploadBytes < 1 {
		errs = append(errs, errors.New("QUIRE_STORAGE_MAX_UPLOAD_BYTES: must be at least 1"))
	}

	chosen := c.Storage.providersConfigured()

	switch len(chosen) {
	case 1:
	case 0:
		return append(errs, errors.New("QUIRE_STORAGE_*: no object store was configured; set one of "+
			"QUIRE_STORAGE_S3_REGION, QUIRE_STORAGE_MINIO_ENDPOINT or QUIRE_STORAGE_GCS_PROJECT_ID"))
	default:
		names := make([]string, 0, len(chosen))
		for _, provider := range chosen {
			names = append(names, string(provider))
		}

		return append(errs, fmt.Errorf("QUIRE_STORAGE_*: %s were all configured; a node holds its "+
			"objects in exactly one store", strings.Join(names, " and ")))
	}

	switch chosen[0] {
	case StorageProviderMinIO:
		errs = append(errs, c.validateStorageMinIO()...)
	case StorageProviderS3:
		errs = append(errs, c.validateStorageS3()...)
	case StorageProviderGCS:
	case StorageProviderNone:
	}

	return errs
}

// validateStorageMinIO checks the section MinIO needs in full. It has no
// credential chain to fall back on, so both halves of the key are required,
// and the endpoint is an authority rather than a URL because that is what the
// SDK takes.
func (c *Config) validateStorageMinIO() []error {
	var errs []error

	minio := &c.Storage.MinIO

	if minio.Endpoint == "" {
		errs = append(errs, errors.New("QUIRE_STORAGE_MINIO_ENDPOINT: required once the MinIO section is used"))
	}

	if strings.Contains(minio.Endpoint, "://") {
		errs = append(errs, fmt.Errorf("QUIRE_STORAGE_MINIO_ENDPOINT: %q must be a host and port, "+
			"without a scheme; use QUIRE_STORAGE_MINIO_USE_TLS to choose one", minio.Endpoint))
	}

	if minio.AccessKeyID == "" || minio.SecretAccessKey == "" {
		errs = append(errs, errors.New(
			"QUIRE_STORAGE_MINIO_ACCESS_KEY_ID and QUIRE_STORAGE_MINIO_SECRET_ACCESS_KEY: both are required"))
	}

	// The bytes of every reader's library cross this connection. In
	// development the MinIO beside the node is plain HTTP, which is why the
	// default is false and why the check is on the profile rather than on the
	// value.
	if !minio.UseTLS && c.Environment.IsProduction() {
		errs = append(errs, errors.New(
			"QUIRE_STORAGE_MINIO_USE_TLS: plain http to the object store is not allowed in production"))
	}

	return errs
}

// validateStorageS3 checks the section S3 needs in full.
//
// The credentials are required rather than optional. The SDK's own chain — an
// instance role, a service account with IRSA — lives in modules this node does
// not depend on, and a node that started without credentials would fail at the
// first import instead of at startup.
func (c *Config) validateStorageS3() []error {
	var errs []error

	s3 := &c.Storage.S3

	if s3.Region == "" {
		errs = append(errs, errors.New("QUIRE_STORAGE_S3_REGION: required once the S3 section is used"))
	}

	if s3.AccessKeyID == "" || s3.SecretAccessKey == "" {
		errs = append(errs, errors.New(
			"QUIRE_STORAGE_S3_ACCESS_KEY_ID and QUIRE_STORAGE_S3_SECRET_ACCESS_KEY: both are required"))
	}

	return errs
}

// The bounds of a TCP port, which is what a relay is addressed by.
const (
	minPort = 1
	maxPort = 65535
)

// validateMail checks the transport the deployment named, and nothing when it
// named none.
//
// A node with no transport is not refused here. It is refused by the adapter
// that would otherwise write a reader's recovery credential to the log, which
// declines to be built outside development — the refusal belongs where the
// substitute is, so that the reason travels with the thing being substituted.
func (c *Config) validateMail() []error {
	if c.Mail.Transport() == MailTransportNone {
		return nil
	}

	var errs []error

	if c.Mail.DeliveryTimeout <= 0 {
		errs = append(errs, errors.New("QUIRE_MAIL_DELIVERY_TIMEOUT: must be positive"))
	}

	if c.Mail.QueueSize < 1 {
		errs = append(errs, errors.New("QUIRE_MAIL_QUEUE_SIZE: must be at least 1"))
	}

	// The envelope sender is checked as an address rather than as a non-empty
	// string, because a relay that rejects it rejects the whole submission and
	// the reader is the one who does not receive the message.
	if c.Mail.FromAddress == "" {
		errs = append(errs, errors.New(
			"QUIRE_MAIL_FROM_ADDRESS: required once a delivery transport is configured"))
	} else if _, err := mail.ParseAddress(c.Mail.FromAddress); err != nil {
		errs = append(errs, fmt.Errorf("QUIRE_MAIL_FROM_ADDRESS: %q is not an address: %w",
			c.Mail.FromAddress, err))
	}

	return append(errs, c.validateMailSMTP()...)
}

// validateMailSMTP checks the section SMTP needs in full.
func (c *Config) validateMailSMTP() []error {
	var errs []error

	smtp := &c.Mail.SMTP

	if smtp.Host == "" {
		errs = append(errs, errors.New("QUIRE_MAIL_SMTP_HOST: required once the SMTP section is used"))
	}

	// SplitHostPort rather than a search for a colon: a bare IPv6 literal is
	// full of them and is a legitimate host, while relay.example:587 is a port
	// written in the wrong variable — and only the second of those splits.
	if _, _, err := net.SplitHostPort(smtp.Host); strings.Contains(smtp.Host, "://") || err == nil {
		errs = append(errs, fmt.Errorf("QUIRE_MAIL_SMTP_HOST: %q must be a bare host, "+
			"without a scheme or a port; use QUIRE_MAIL_SMTP_PORT for the port", smtp.Host))
	}

	if smtp.Port < minPort || smtp.Port > maxPort {
		errs = append(errs, fmt.Errorf("QUIRE_MAIL_SMTP_PORT: %d is outside the range %d to %d",
			smtp.Port, minPort, maxPort))
	}

	// One half of a credential is a submission the relay refuses, and it
	// refuses it after the connection is up, which is the slowest way to
	// discover a typo.
	if (smtp.Username == "") != smtp.Password.IsZero() {
		errs = append(errs, errors.New(
			"QUIRE_MAIL_SMTP_USERNAME and QUIRE_MAIL_SMTP_PASSWORD: set both or neither"))
	}

	switch smtp.Security {
	case MailSecurityStartTLS, MailSecurityTLS:
	case MailSecurityNone:
		// The recovery credential is what replaces a password, so it is held
		// to the same standard the password itself is: the check is on the
		// profile rather than on the value, because the relay a developer runs
		// beside the node speaks no TLS at all.
		if c.Environment.IsProduction() {
			errs = append(errs, errors.New(
				"QUIRE_MAIL_SMTP_SECURITY: submitting a recovery credential in the clear "+
					"is not allowed in production"))
		}
	default:
		errs = append(errs, fmt.Errorf("QUIRE_MAIL_SMTP_SECURITY: %q must be one of %s, %s, %s",
			smtp.Security, MailSecurityStartTLS, MailSecurityTLS, MailSecurityNone))
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
