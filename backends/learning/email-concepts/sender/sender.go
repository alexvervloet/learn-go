// Package sender submits email over SMTP, and the four things net/smtp does not do for you.
//
// # What net/smtp is
//
// It is frozen. The package documentation says so: "the package is frozen and is not accepting new features".
// It does PLAIN and CRAM-MD5 authentication, STARTTLS, and a Client with the individual SMTP verbs. It does not
// do OAUTH2 (which Gmail and Office 365 now require), connection pooling, or retries.
//
// So a service sending through Gmail needs a third-party library or its own XOAUTH2 implementation, and a service
// sending through SES or SendGrid over their HTTP APIs does not use net/smtp at all. This package is what you
// need for a self-hosted relay, a local Mailpit, or a provider that still accepts SMTP with a password.
//
// # The four
//
//	TLS            smtp.SendMail does STARTTLS when the server advertises it and sends in the CLEAR
//	               when it does not, with no error. So a misconfigured server means credentials on
//	               the wire, and nothing reports it.
//	AUTH ORDER     smtp.PlainAuth REFUSES to send over an unencrypted connection, which is right
//	               and produces the confusing error "unencrypted connection" when STARTTLS failed
//	               for an unrelated reason.
//	ENVELOPE       the RCPT TO list is not the To header. Bcc lives only in the envelope, and a
//	               message whose header list and envelope list disagree is normal rather than a bug.
//	TIMEOUTS       smtp.SendMail has none. A connection to a black hole hangs forever, and there is
//	               no option: the timeout has to come from a net.Dialer and a manually driven Client.
package sender

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/email-concepts/message"
)

// Errors a caller distinguishes.
//
// The 4xx/5xx split is the one that matters: a 4xx is temporary and worth retrying, a 5xx is permanent and
// retrying it is how a mail server decides you are a spammer.
var (
	ErrTemporary = errors.New("temporary SMTP failure")
	ErrPermanent = errors.New("permanent SMTP failure")
	ErrNoTLS     = errors.New("the server does not support STARTTLS")
)

// Config is an SMTP sender's settings.
type Config struct {
	Host string
	Port int

	Username string
	Password string

	// RequireTLS refuses to send in the clear.
	//
	// The default has to be true, and smtp.SendMail's behaviour is the reason: it uses STARTTLS when
	// the server advertises it and sends in the clear when it does not, silently. So a server that
	// stops advertising STARTTLS after a certificate expires starts sending every message and every
	// password in plaintext, and nothing reports it.
	RequireTLS bool

	// Timeout bounds the whole conversation. net/smtp has no timeout at all, so this is enforced by
	// dialling with a net.Dialer and driving the Client by hand.
	Timeout time.Duration

	// TLSConfig is passed to StartTLS. Nil uses the host name for verification, which is correct;
	// InsecureSkipVerify is what people set to make a self-signed development server work and then
	// ship.
	TLSConfig *tls.Config
}

// DefaultConfig is secure rather than permissive.
func DefaultConfig(host string, port int) Config {
	return Config{
		Host:       host,
		Port:       port,
		RequireTLS: true,
		Timeout:    30 * time.Second,
	}
}

// Addr returns the host:port.
func (c Config) Addr() string { return net.JoinHostPort(c.Host, fmt.Sprint(c.Port)) }

// Sender submits messages.
type Sender struct {
	cfg Config

	sent   atomic.Int64
	failed atomic.Int64
}

// New builds one.
func New(cfg Config) *Sender {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}

	return &Sender{cfg: cfg}
}

// Send submits one message.
//
// # Why this drives the Client rather than calling smtp.SendMail
//
// smtp.SendMail is four lines and does the whole conversation, and it has no timeout and no way to require TLS.
// Driving the Client is thirty lines and makes both possible, which for a service that sends mail from a request
// handler is the difference between a slow endpoint and a hung one.
func (s *Sender) Send(ctx context.Context, m *message.Message) error {
	raw, err := m.Bytes()
	if err != nil {
		s.failed.Add(1)
		return fmt.Errorf("building the message: %w", err)
	}

	// The ENVELOPE recipients, which include Bcc. The headers do not.
	recipients := m.Recipients()

	if err := s.deliver(ctx, m.From.Email, recipients, raw); err != nil {
		s.failed.Add(1)
		return err
	}

	s.sent.Add(1)

	return nil
}

// deliver does the SMTP conversation.
func (s *Sender) deliver(ctx context.Context, from string, to []string, raw []byte) error {
	// A dialler with a timeout AND a context, so a cancelled request stops the dial. net/smtp takes
	// neither, which is why the connection is made here and handed to smtp.NewClient.
	dialer := &net.Dialer{Timeout: s.cfg.Timeout}

	conn, err := dialer.DialContext(ctx, "tcp", s.cfg.Addr())
	if err != nil {
		return fmt.Errorf("%w: dialling %s: %w", ErrTemporary, s.cfg.Addr(), err)
	}

	// A deadline on the CONNECTION, which is what bounds the rest of the conversation. The dialler's
	// timeout covers only the dial; without this a server that accepts the connection and then says
	// nothing hangs forever.
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			_ = conn.Close()
			return fmt.Errorf("setting the connection deadline: %w", err)
		}
	} else if err := conn.SetDeadline(time.Now().Add(s.cfg.Timeout)); err != nil {
		_ = conn.Close()
		return fmt.Errorf("setting the connection deadline: %w", err)
	}

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("%w: SMTP handshake: %w", ErrTemporary, err)
	}

	// Close, not Quit, in the deferred cleanup. Quit sends QUIT and waits for a reply, which on a
	// broken connection blocks; Close just drops the socket. The happy path calls Quit explicitly.
	defer func() { _ = client.Close() }()

	if err := s.startTLS(client); err != nil {
		return err
	}

	if s.cfg.Username != "" {
		// PlainAuth with the host name, which it checks against the connection: it REFUSES to
		// send credentials over an unencrypted connection. That produces the confusing error
		// "unencrypted connection" when STARTTLS failed for an unrelated reason, and it is the
		// right behaviour.
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)

		if err := client.Auth(auth); err != nil {
			// Authentication failure is PERMANENT: the password will not become correct on a
			// retry, and retrying it gets the account locked.
			return fmt.Errorf("%w: authenticating as %s: %w", ErrPermanent, s.cfg.Username, err)
		}
	}

	if err := client.Mail(from); err != nil {
		return classify("MAIL FROM", err)
	}

	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			// One bad recipient does not have to fail the whole message, and this
			// implementation makes it do so. The alternative is to collect the failures and
			// send to the rest, which is what a bulk sender does and is wrong for a
			// transactional one: a password reset that went to three of four addresses is a
			// failure the caller has to know about.
			return classify("RCPT TO "+rcpt, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return classify("DATA", err)
	}

	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("%w: writing the message: %w", ErrTemporary, err)
	}

	// Close is what ends the DATA phase and is where the server accepts or rejects the message. A
	// sender that ignores this error reports success for a message the server refused, which is the
	// worst possible outcome: no bounce, no log, no email.
	if err := w.Close(); err != nil {
		return classify("end of DATA", err)
	}

	if err := client.Quit(); err != nil {
		// A failed QUIT after a successful DATA is not a failed send: the server already accepted
		// the message. Treating it as an error makes a caller retry and send twice.
		return nil
	}

	return nil
}

// startTLS upgrades the connection, or refuses.
func (s *Sender) startTLS(client *smtp.Client) error {
	ok, _ := client.Extension("STARTTLS")

	if !ok {
		if s.cfg.RequireTLS {
			return fmt.Errorf("%w: %s", ErrNoTLS, s.cfg.Addr())
		}

		return nil
	}

	cfg := s.cfg.TLSConfig
	if cfg == nil {
		// ServerName set from the config, so the certificate is verified against the name we
		// dialled. Leaving it empty makes crypto/tls use the address, which for an IP address
		// fails, and setting InsecureSkipVerify is what people do about that.
		cfg = &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	}

	if err := client.StartTLS(cfg); err != nil {
		return fmt.Errorf("%w: STARTTLS: %w", ErrTemporary, err)
	}

	return nil
}

// classify turns an SMTP error into a temporary or permanent one.
//
// # Why the first digit
//
// SMTP reply codes are three digits and the FIRST one is the class: 2xx is success, 4xx is a transient failure
// ("try again later"), 5xx is permanent ("do not try again"). A sender that retries a 5xx is a sender that mail
// providers start rejecting, and one that gives up on a 4xx loses mail that would have been delivered.
//
// net/smtp returns a *textproto.Error for a server reply, which carries the code. Anything else is a transport
// failure and is temporary by definition: the connection broke, so the server never said anything.
func classify(op string, err error) error {
	var protoErr *textproto.Error

	if errors.As(err, &protoErr) {
		switch protoErr.Code / 100 {
		case 4:
			return fmt.Errorf("%w: %s: %d %s", ErrTemporary, op, protoErr.Code, protoErr.Msg)
		case 5:
			return fmt.Errorf("%w: %s: %d %s", ErrPermanent, op, protoErr.Code, protoErr.Msg)
		}
	}

	return fmt.Errorf("%w: %s: %w", ErrTemporary, op, err)
}

// Stats reports what the sender counted.
func (s *Sender) Stats() (sent, failed int64) { return s.sent.Load(), s.failed.Load() }

// Recorder is a Transport that records instead of sending, for tests that do not need a server.
//
// # Why both this and a real server
//
// A recorder tests the CALLER: that a handler built the right message, to the right addresses, with the right
// template data. It cannot test the message's MIME structure, because nothing parses it.
//
// A real SMTP server (Mailpit) tests the MESSAGE: that it parses, that the parts are in the right order, that
// the attachment decodes. It needs Docker.
//
// A suite wants both, and using only the recorder is how a service ships a message no client can render.
type Recorder struct {
	mu sync.Mutex

	Sent []Recorded

	// Err makes the next send fail, for testing the caller's error handling.
	Err error
}

// Recorded is one captured send.
type Recorded struct {
	From       string
	Recipients []string
	Raw        []byte
}

// Send records a message.
func (r *Recorder) Send(_ context.Context, m *message.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.Err != nil {
		return r.Err
	}

	raw, err := m.Bytes()
	if err != nil {
		return fmt.Errorf("building the message: %w", err)
	}

	r.Sent = append(r.Sent, Recorded{
		From:       m.From.Email,
		Recipients: m.Recipients(),
		Raw:        raw,
	})

	return nil
}

// Messages returns a copy.
func (r *Recorder) Messages() []Recorded {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]Recorded(nil), r.Sent...)
}

// Reset clears it.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.Sent = nil
	r.Err = nil
}

// Transport is what a service depends on, so a test can substitute the Recorder.
//
// An interface declared HERE, at the consumer, rather than in the sender: the same decision as every other
// interface in this repo, and the reason is that a service needs one method and Sender has five.
type Transport interface {
	Send(ctx context.Context, m *message.Message) error
}

var (
	_ Transport = (*Sender)(nil)
	_ Transport = (*Recorder)(nil)
)
