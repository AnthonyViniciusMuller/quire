// Package smtp delivers a password recovery by submitting it to a relay.
//
// It is the transport C13 in docs/tcc-corrections.md says the architecture is
// missing: RF09 requires a credential to reach a reader's address, and until
// this package existed the only adapter of the port wrote the credential to
// the log and refused to be built outside development — which is why a node
// could not start under QUIRE_ENV=production at all.
//
// It is written against net/smtp rather than against a mail library, and the
// measurement is in the commit that introduced it: what a library would
// replace here is four standard-library calls — an address encoded by
// net/mail, a header set encoded by mime, a body encoded by
// mime/quotedprintable, and a submission by net/smtp — for one plain-text
// message with no attachments, no alternatives and no templating. That is the
// shape of trade this project has refused before, and the opposite of the one
// it accepted for the object store, where the vendor publishes the client.
//
// What net/smtp does not do is take a context, so this package dials with one
// and then converts it into a deadline on the connection. That is the whole of
// the honesty available: a cancelled context stops the submission at the next
// read or write rather than at the instant it was cancelled.
package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opNew  = "identity/smtp: new"
	opSend = "identity/smtp: send password recovery"
)

// minimumTLSVersion is what this node will negotiate with a relay. A recovery
// credential replaces a password, so the connection carrying it is held to the
// version the rest of the node is.
const minimumTLSVersion = uint16(tls.VersionTLS12)

// Service submits messages to a relay.
type Service struct {
	// from is the envelope sender, parsed once so that a malformed address is
	// a node that does not start rather than a submission a relay refuses.
	from *mail.Address
	// host and port address the relay. They are kept apart because the TLS
	// server name is the host alone.
	host string
	port int
	// auth is nil when the relay accepts this node without credentials.
	auth smtp.Auth
	// security is how the connection is protected.
	security config.MailSecurity
	// timeout bounds one submission.
	timeout time.Duration
	// serverName is the federation domain of this node. It is the right-hand
	// side of the Message-ID, which is what makes an identifier this node
	// mints globally unique without a registry.
	serverName string
}

// Service satisfies the port the use cases hold.
var _ service.Mailer = (*Service)(nil)

// New returns the adapter over the mail section of the configuration.
//
// Nothing is dialled. A relay that is down is a delivery that fails, not a
// node that refuses to start — the node can still serve every other call, and
// UC08 is the one that degrades.
func New(cfg *config.Mail, serverName string) (*Service, error) {
	from, err := mail.ParseAddress(cfg.FromAddress)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInvalidArgument, "the sender address cannot be used").
			WithOp(opNew).
			WithField("QUIRE_MAIL_FROM_ADDRESS", "it must be an address a relay will accept as the sender")
	}

	if cfg.FromName != "" {
		from.Name = cfg.FromName
	}

	var auth smtp.Auth
	if cfg.SMTP.Username != "" {
		// PlainAuth of its own accord refuses to hand credentials to a
		// connection that is not encrypted, unless the relay is on this
		// machine. That refusal is kept: a deployment that wanted to
		// authenticate in the clear has asked for the one thing this package
		// exists to avoid.
		auth = smtp.PlainAuth("", cfg.SMTP.Username, cfg.SMTP.Password.Reveal(), cfg.SMTP.Host)
	}

	return &Service{
		from:       from,
		host:       cfg.SMTP.Host,
		port:       cfg.SMTP.Port,
		auth:       auth,
		security:   cfg.SMTP.Security,
		timeout:    cfg.DeliveryTimeout,
		serverName: serverName,
	}, nil
}

// SendPasswordRecovery submits the credential to the address on record.
func (s *Service) SendPasswordRecovery(ctx context.Context, message service.RecoveryMessage) error {
	to := &mail.Address{
		Name:    message.DisplayName.String(),
		Address: message.Email.String(),
	}

	body, err := s.compose(to, message)
	if err != nil {
		return err
	}

	return s.submit(ctx, to.Address, body)
}

// compose renders the message, headers and all.
//
// The credential is in the body and never in a header, because headers are
// what a relay logs and what a bounce quotes back.
func (s *Service) compose(to *mail.Address, message service.RecoveryMessage) ([]byte, error) {
	var out strings.Builder

	// Address.String encodes a display name that is not ASCII, which is not a
	// corner case here: the readers this is written for have names with
	// accents in them.
	headers := [][2]string{
		{"From", s.from.String()},
		{"To", to.String()},
		{"Subject", encodeHeader(subject)},
		{"Date", time.Now().Format(time.RFC1123Z)},
		{"Message-ID", s.messageID()},
		{"MIME-Version", "1.0"},
		{"Content-Type", `text/plain; charset="utf-8"`},
		{"Content-Transfer-Encoding", "quoted-printable"},
		// A recovery is not a newsletter, and a mailbox that files it as one is
		// a reader who cannot get back in.
		{"Auto-Submitted", "auto-generated"},
	}

	for _, header := range headers {
		out.WriteString(header[0])
		out.WriteString(": ")
		out.WriteString(header[1])
		out.WriteString("\r\n")
	}

	out.WriteString("\r\n")

	// Quoted-printable rather than raw UTF-8: it keeps every line inside the
	// 998 octets RFC 5322 allows, which a display name and a token concatenated
	// into a sentence would not be guaranteed to.
	encoder := quotedprintable.NewWriter(&out)
	if _, err := io.WriteString(encoder, body(message)); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "the recovery message could not be encoded").
			WithOp(opSend)
	}

	if err := encoder.Close(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "the recovery message could not be encoded").
			WithOp(opSend)
	}

	return []byte(out.String()), nil
}

// messageID mints the identifier of this message.
//
// The right-hand side is the federation domain of the node, which is the one
// name this process owns; the left-hand side is a random value, because a
// counter would tell a relay how many recoveries this node has issued.
func (s *Service) messageID() string {
	return fmt.Sprintf("<%s@%s>", newIdentifier(), s.serverName)
}

// submit opens the connection, hands over the message and closes it.
//
// It is one connection per message. Pooling would be worth it for a node that
// sent in bulk; this one sends a recovery, which is rare by construction, and a
// pooled connection to a relay is one more thing that can be half-open.
func (s *Service) submit(ctx context.Context, to string, body []byte) error {
	connection, err := s.dial(ctx)
	if err != nil {
		return err
	}

	client, err := smtp.NewClient(connection, s.host)
	if err != nil {
		_ = connection.Close()

		return s.failure(err, "the relay did not greet this node")
	}

	// Quit closes the connection on the way out; Close is the fallback for a
	// session that failed before Quit could be reached, and is a no-op after it.
	defer func() { _ = client.Close() }()

	if err := s.upgrade(client); err != nil {
		return err
	}

	if s.auth != nil {
		if err := client.Auth(s.auth); err != nil {
			return s.failure(err, "the relay refused this node's credentials")
		}
	}

	if err := s.exchange(client, to, body); err != nil {
		return err
	}

	if err := client.Quit(); err != nil {
		return s.failure(err, "the relay did not accept the end of the session")
	}

	return nil
}

// dial opens the transport under the caller's context and puts a deadline on
// it, which is how a package that takes no context is made to honour one.
func (s *Service) dial(ctx context.Context) (net.Conn, error) {
	address := net.JoinHostPort(s.host, strconv.Itoa(s.port))

	dialer := &net.Dialer{}

	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, s.failure(err, "the relay could not be reached")
	}

	deadline := time.Now().Add(s.timeout)
	if fromContext, ok := ctx.Deadline(); ok && fromContext.Before(deadline) {
		deadline = fromContext
	}

	if err := connection.SetDeadline(deadline); err != nil {
		_ = connection.Close()

		return nil, s.failure(err, "the connection to the relay could not be bounded")
	}

	if s.security != config.MailSecurityTLS {
		return connection, nil
	}

	// Implicit TLS, which is what port 465 expects. The handshake is explicit
	// rather than left to the first read, so that a relay presenting a
	// certificate this node cannot verify fails here and says so.
	secure := tls.Client(connection, &tls.Config{ServerName: s.host, MinVersion: minimumTLSVersion})
	if err := secure.HandshakeContext(ctx); err != nil {
		_ = connection.Close()

		return nil, s.failure(err, "the relay's certificate could not be verified")
	}

	return secure, nil
}

// upgrade runs STARTTLS when that is how this relay is protected.
//
// A relay that does not advertise the extension is refused rather than
// submitted to in the clear: a deployment that meant to submit in the clear
// says so, and the configuration refuses to say it in production.
func (s *Service) upgrade(client *smtp.Client) error {
	if s.security != config.MailSecurityStartTLS {
		return nil
	}

	if ok, _ := client.Extension("STARTTLS"); !ok {
		return errs.New(errs.KindFailedPrecondition,
			"the relay does not offer STARTTLS, and this node will not submit a recovery credential in the clear").
			WithOp(opSend)
	}

	err := client.StartTLS(&tls.Config{ServerName: s.host, MinVersion: minimumTLSVersion})
	if err != nil {
		return s.failure(err, "the connection to the relay could not be secured")
	}

	return nil
}

// exchange is the envelope and the message.
func (s *Service) exchange(client *smtp.Client, to string, body []byte) error {
	if err := client.Mail(s.from.Address); err != nil {
		return s.failure(err, "the relay refused the sender")
	}

	if err := client.Rcpt(to); err != nil {
		return s.failure(err, "the relay refused the recipient")
	}

	writer, err := client.Data()
	if err != nil {
		return s.failure(err, "the relay refused to take the message")
	}

	if _, err := writer.Write(body); err != nil {
		_ = writer.Close()

		return s.failure(err, "the message could not be written to the relay")
	}

	if err := writer.Close(); err != nil {
		return s.failure(err, "the relay did not accept the message")
	}

	return nil
}

// failure is every error this package reports to the caller.
//
// It is Unavailable rather than Internal: a relay that is down, refusing or
// unreachable is a dependency that is not answering, and the caller's own
// documentation says the reader loses one attempt and may repeat it. The
// recipient is never named — the error travels to a log, and an error naming
// the address would put in the log exactly what the empty reply keeps out of
// the response.
func (s *Service) failure(err error, message string) error {
	return errs.Wrap(err, errs.KindUnavailable, message).
		WithOp(opSend).
		WithField("relay", net.JoinHostPort(s.host, strconv.Itoa(s.port)))
}
