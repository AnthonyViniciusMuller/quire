package smtp_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime/quotedprintable"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	smtpservice "github.com/anthonyvsmuller/quire/internal/identity/infra/service/smtp"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// theMessage is what every test here delivers. The display name carries an
// accent on purpose: the readers this is written for have them, and a header
// that shipped one raw would be a header a relay may reject.
func theMessage() service.RecoveryMessage {
	return service.RecoveryMessage{
		Email:       user.Email("antônio@quire-a.example"),
		DisplayName: user.DisplayName("Antônio Müller"),
		Token:       "recovery-token-01HZ",
		ExpiresAt:   time.Date(2026, time.August, 28, 15, 4, 0, 0, time.UTC),
	}
}

// relay is a fake SMTP server: it speaks enough of the dialogue for a
// submission to complete, and keeps what it was handed.
//
// It is a listener rather than a container because what is under test is the
// message this package composes and the order it submits in, and neither of
// those needs a real relay to be checked. What a real relay would add is its
// own policy, which is not this package's to satisfy.
type relay struct {
	// address is where the adapter reaches it.
	address string
	// advertiseStartTLS decides whether the greeting offers the extension the
	// adapter refuses to submit without.
	advertiseStartTLS bool
	// received is the message, once one arrives.
	received chan string
}

func newRelay(t *testing.T, advertiseStartTLS bool) *relay {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	fake := &relay{
		address:           listener.Addr().String(),
		advertiseStartTLS: advertiseStartTLS,
		received:          make(chan string, 1),
	}

	go fake.accept(listener)

	return fake
}

func (r *relay) accept(listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}

		go r.serve(connection)
	}
}

// serve is the dialogue, and only as much of it as a submission uses.
func (r *relay) serve(connection net.Conn) {
	defer func() { _ = connection.Close() }()

	reader := bufio.NewReader(connection)
	write := func(line string) { _, _ = connection.Write([]byte(line + "\r\n")) }

	write("220 relay.test ESMTP")

	var body strings.Builder

	inData := false

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		line = strings.TrimRight(line, "\r\n")

		if inData {
			if line == "." {
				inData = false

				r.received <- body.String()

				write("250 2.0.0 Ok")

				continue
			}

			body.WriteString(line + "\n")

			continue
		}

		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			write("250-relay.test")

			if r.advertiseStartTLS {
				write("250-STARTTLS")
			}

			write("250 SIZE 10240000")
		case strings.HasPrefix(line, "MAIL FROM"), strings.HasPrefix(line, "RCPT TO"):
			write("250 2.1.0 Ok")
		case line == "DATA":
			inData = true

			write("354 End data with <CR><LF>.<CR><LF>")
		case line == "QUIT":
			write("221 2.0.0 Bye")

			return
		default:
			write("250 2.0.0 Ok")
		}
	}
}

// deliveredTo builds an adapter pointed at the fake relay.
func deliveredTo(t *testing.T, fake *relay, security config.MailSecurity) *smtpservice.Service {
	t.Helper()

	host, port, err := net.SplitHostPort(fake.address)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v, want nil", err)
	}

	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("Atoi() error = %v, want nil", err)
	}

	sender, err := smtpservice.New(&config.Mail{
		FromAddress:     "no-reply@quire-a.example",
		FromName:        "Quire",
		DeliveryTimeout: 5 * time.Second,
		SMTP: config.MailSMTP{
			Host:     host,
			Port:     number,
			Security: security,
		},
	}, "quire-a.example")
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}

	return sender
}

func TestSendPasswordRecoverySubmitsTheMessage(t *testing.T) {
	t.Parallel()

	fake := newRelay(t, false)
	sender := deliveredTo(t, fake, config.MailSecurityNone)

	if err := sender.SendPasswordRecovery(t.Context(), theMessage()); err != nil {
		t.Fatalf("SendPasswordRecovery() error = %v, want nil", err)
	}

	submitted := waitFor(t, fake)
	headers, body := split(t, submitted)

	tests := []struct {
		name   string
		got    string
		want   string
		inBody bool
	}{
		{name: "the sender", got: headers, want: "no-reply@quire-a.example"},
		{name: "the recipient", got: headers, want: "antônio@quire-a.example"},
		{name: "the content type", got: headers, want: `text/plain; charset="utf-8"`},
		{name: "the node's own domain in the message id", got: headers, want: "@quire-a.example>"},
		{name: "the reader's name", got: body, want: "Antônio Müller", inBody: true},
		{name: "the credential", got: body, want: "recovery-token-01HZ", inBody: true},
		{name: "when it stops working", got: body, want: "28 August 2026", inBody: true},
	}

	for _, test := range tests {
		if !strings.Contains(test.got, test.want) {
			t.Errorf("%s: %q is not in the %s", test.name, test.want,
				map[bool]string{true: "body", false: "headers"}[test.inBody])
		}
	}
}

// A subject line is what a locked screen shows, and a credential there is a
// credential shown to whoever is holding the device. The headers are also what
// a relay logs and what a bounce quotes back.
func TestTheCredentialIsInTheBodyAndNowhereElse(t *testing.T) {
	t.Parallel()

	fake := newRelay(t, false)
	sender := deliveredTo(t, fake, config.MailSecurityNone)

	if err := sender.SendPasswordRecovery(t.Context(), theMessage()); err != nil {
		t.Fatalf("SendPasswordRecovery() error = %v, want nil", err)
	}

	headers, _ := split(t, waitFor(t, fake))

	if strings.Contains(headers, "recovery-token-01HZ") {
		t.Errorf("the headers carry the credential:\n%s", headers)
	}
}

// A name that is not ASCII travels encoded, per RFC 2047. Shipping it raw is a
// header a relay may refuse, and refusing it is the reader not receiving the
// message at all.
func TestANameThatIsNotASCIITravelsEncoded(t *testing.T) {
	t.Parallel()

	fake := newRelay(t, false)
	sender := deliveredTo(t, fake, config.MailSecurityNone)

	if err := sender.SendPasswordRecovery(t.Context(), theMessage()); err != nil {
		t.Fatalf("SendPasswordRecovery() error = %v, want nil", err)
	}

	headers, _ := split(t, waitFor(t, fake))

	toHeader := headerNamed(t, headers, "To")
	if !strings.Contains(toHeader, "=?utf-8?") && !strings.Contains(toHeader, "=?UTF-8?") {
		t.Errorf("To = %q, want the display name encoded", toHeader)
	}
}

// The refusal is the point of naming starttls: a relay that does not offer it
// is one this node would have to submit to in the clear, and the credential it
// is carrying is the one that replaces a password.
func TestARelayWithoutStartTLSIsRefused(t *testing.T) {
	t.Parallel()

	fake := newRelay(t, false)
	sender := deliveredTo(t, fake, config.MailSecurityStartTLS)

	err := sender.SendPasswordRecovery(t.Context(), theMessage())
	if !errors.Is(err, errs.KindFailedPrecondition) {
		t.Fatalf("SendPasswordRecovery() error = %v, want a failed precondition", err)
	}

	select {
	case submitted := <-fake.received:
		t.Errorf("the message was submitted anyway:\n%s", submitted)
	default:
	}
}

// A relay that is not there is a dependency that is not answering, which the
// caller logs and the reader repeats — never an internal error, and never one
// that names the address it was going to.
func TestARelayThatIsNotThereIsUnavailable(t *testing.T) {
	t.Parallel()

	fake := newRelay(t, false)
	sender := deliveredTo(t, fake, config.MailSecurityNone)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := sender.SendPasswordRecovery(ctx, theMessage())
	if !errors.Is(err, errs.KindUnavailable) {
		t.Fatalf("SendPasswordRecovery() error = %v, want unavailable", err)
	}

	if strings.Contains(err.Error(), "antônio@quire-a.example") {
		t.Errorf("the error names the recipient: %v", err)
	}
}

// waitFor returns the message the relay was handed, or fails saying it was
// handed none.
func waitFor(t *testing.T, fake *relay) string {
	t.Helper()

	select {
	case submitted := <-fake.received:
		return submitted
	case <-time.After(5 * time.Second):
		t.Fatal("the relay was handed no message")

		return ""
	}
}

// split separates the headers from the body and decodes the body, which
// travels quoted-printable.
func split(t *testing.T, submitted string) (headers, body string) {
	t.Helper()

	headers, encoded, found := strings.Cut(submitted, "\n\n")
	if !found {
		t.Fatalf("the message has no body:\n%s", submitted)
	}

	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(encoded)))
	if err != nil {
		t.Fatalf("decoding the body: %v", err)
	}

	return headers, string(decoded)
}

// headerNamed returns one header, unfolded.
func headerNamed(t *testing.T, headers, name string) string {
	t.Helper()

	for _, line := range strings.Split(headers, "\n") {
		if strings.HasPrefix(line, name+": ") {
			return strings.TrimPrefix(line, name+": ")
		}
	}

	t.Fatalf("there is no %s header in:\n%s", name, headers)

	return ""
}
