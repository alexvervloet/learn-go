package sender_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/email-concepts/mailpit"
	"github.com/alexvervloet/learn-go/backends/learning/email-concepts/message"
	"github.com/alexvervloet/learn-go/backends/learning/email-concepts/sender"
	"github.com/alexvervloet/learn-go/backends/learning/email-concepts/templates"
)

func mailpitSender(t *testing.T) *sender.Sender {
	t.Helper()

	mailpit.Require(t)
	mailpit.Clear(t)

	cfg := sender.DefaultConfig(mailpit.SMTPHost(), mailpit.SMTPPort())

	// Mailpit does not offer STARTTLS on its plain port, so a local sender has to allow plaintext.
	// That is exactly the configuration a production sender must NOT have, which is why RequireTLS
	// defaults to true and this is an explicit override.
	cfg.RequireTLS = false
	cfg.Timeout = 10 * time.Second

	return sender.New(cfg)
}

// TestSendAndReadBack is the end-to-end: a real SMTP conversation, then read the parsed result.
func TestSendAndReadBack(t *testing.T) {
	s := mailpitSender(t)

	renderer, err := templates.New()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("templates: %v", renderer.Names())

	text, html, err := renderer.Welcome(templates.WelcomeData{
		Name:       "Ada Lovelace",
		Plan:       "Pro",
		ConfirmURL: "https://example.test/confirm?t=abc123",
		Trial:      true,
		TrialEnds:  time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),
		Footer:     "You are receiving this because you signed up.",
	})
	if err != nil {
		t.Fatal(err)
	}

	m := &message.Message{
		From:    message.Address{Name: "Learn Go", Email: "noreply@example.test"},
		To:      []message.Address{{Name: "Ada Lovelace", Email: "ada@example.test"}},
		Cc:      []message.Address{{Email: "records@example.test"}},
		Bcc:     []message.Address{{Email: "audit@example.test"}},
		Subject: "Welcome to Learn Go ☕",
		Text:    text,
		HTML:    html,
		Attachments: []message.Attachment{{
			Filename:    "getting started.pdf",
			ContentType: "application/pdf",
			Content:     []byte("%PDF-1.4 pretend"),
		}},
		Headers: map[string]string{
			"List-Unsubscribe": "<https://example.test/unsubscribe?t=abc>",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := s.Send(ctx, m); err != nil {
		t.Fatal(err)
	}

	summaries := mailpit.WaitForCount(t, 1, 10*time.Second)

	got := mailpit.Get(t, summaries[0].ID)

	t.Logf("subject:  %q", got.Subject)
	t.Logf("from:     %s <%s>", got.From.Name, got.From.Address)
	t.Logf("to:       %v", addresses(got.To))
	t.Logf("cc:       %v", addresses(got.Cc))
	t.Logf("bcc:      %v", addresses(got.Bcc))
	t.Logf("text:     %d bytes", len(got.Text))
	t.Logf("html:     %d bytes", len(got.HTML))
	t.Logf("attachments: %d", len(got.Attachments))

	// The subject was Q-encoded on the wire and Mailpit decoded it, which is the round trip that
	// matters: a client sees the original.
	if got.Subject != "Welcome to Learn Go ☕" {
		t.Errorf("the subject arrived as %q", got.Subject)
	}

	// The display name survived.
	if got.To[0].Name != "Ada Lovelace" {
		t.Errorf("the recipient name arrived as %q", got.To[0].Name)
	}

	// Both bodies are there and decoded.
	if !strings.Contains(got.Text, "Hello Ada Lovelace") {
		t.Errorf("the text body is wrong:\n%s", got.Text)
	}
	if !strings.Contains(got.HTML, "<strong>Pro</strong>") {
		t.Errorf("the HTML body is wrong:\n%s", got.HTML)
	}

	// The quoted-printable was decoded, so the URL's = signs are back.
	if !strings.Contains(got.Text, "https://example.test/confirm?t=abc123") {
		t.Errorf("the URL did not survive quoted-printable:\n%s", got.Text)
	}

	// The attachment, with its filename and its bytes.
	if len(got.Attachments) != 1 {
		t.Fatalf("got %d attachments", len(got.Attachments))
	}

	if got.Attachments[0].FileName != "getting started.pdf" {
		t.Errorf("the filename arrived as %q", got.Attachments[0].FileName)
	}

	content := mailpit.AttachmentContent(t, got.ID, got.Attachments[0].PartID)

	if string(content) != "%PDF-1.4 pretend" {
		t.Errorf("the attachment content is %q", content)
	}

	t.Logf("the attachment round-tripped: %q", content)

	// THE BCC, and a finding about the tool rather than the code.
	//
	// Mailpit reports the Bcc, which it can only know from the envelope, and it also PREPENDS a Bcc
	// header to the raw message it stores, along with Message-ID, Return-Path and Received.
	//
	// So Mailpit's "raw" is not what was on the wire. Asserting the absence of a Bcc header against
	// it fails, which is what happened first time and looked like a bug in the builder. The message
	// bytes are the only place that question can be answered, and message_test.go is where it is.
	raw := mailpit.Raw(t, got.ID)

	addedByMailpit := []string{"Bcc:", "Message-ID:", "Return-Path:", "Received:"}

	var added []string

	for _, header := range addedByMailpit {
		if strings.Contains(raw, header) {
			added = append(added, strings.TrimSuffix(header, ":"))
		}
	}

	t.Logf("headers Mailpit added to the stored message: %v", added)

	// What the builder sent, checked against the builder's own output.
	built, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(built), "audit@example.test") {
		t.Error("the builder put the Bcc address in the message")
	}

	t.Logf("Mailpit reports the Bcc as %v because it saw the envelope, and the message the "+
		"builder produced does not contain it anywhere", addresses(got.Bcc))

	// And the envelope DID carry it, which is the round trip that proves Bcc works.
	found := false
	for _, c := range got.Bcc {
		if c.Address == "audit@example.test" {
			found = true
		}
	}

	if !found {
		t.Error("the Bcc recipient did not receive the message")
	}

	t.Log("the Bcc recipient received it and no recipient can see the list, which is the whole " +
		"of what Bcc means and is entirely a property of the ENVELOPE")

	t.Log("a recorder would have proved the message was built; only a real server and a real " +
		"parse prove it is VALID. This one caught the difference between a Bcc in the envelope " +
		"and a Bcc in the headers, which a recorder cannot see.")
}

func addresses(contacts []mailpit.Contact) []string {
	out := make([]string, len(contacts))
	for i, c := range contacts {
		out[i] = c.Address
	}
	return out
}

// TestRequireTLSRefusesPlaintext is the default that matters.
func TestRequireTLSRefusesPlaintext(t *testing.T) {
	mailpit.Require(t)

	// The DEFAULT config, which requires TLS.
	cfg := sender.DefaultConfig(mailpit.SMTPHost(), mailpit.SMTPPort())
	cfg.Timeout = 5 * time.Second

	if !cfg.RequireTLS {
		t.Fatal("DefaultConfig does not require TLS")
	}

	s := sender.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := s.Send(ctx, plainMessage())

	t.Logf("sending to a server with no STARTTLS, with RequireTLS: %v", err)

	if !errors.Is(err, sender.ErrNoTLS) {
		t.Errorf("got %v, want ErrNoTLS", err)
	}

	t.Log("smtp.SendMail would have sent this in the clear, with no error. So a server that stops " +
		"advertising STARTTLS after a certificate expires starts sending every message and " +
		"every password in plaintext, and nothing reports it. RequireTLS defaulting to true is " +
		"the only defence.")
}

// TestTimeoutsAreEnforced, because net/smtp has none.
func TestTimeoutsAreEnforced(t *testing.T) {
	// A port nothing listens on, so the dial hangs or is refused.
	cfg := sender.DefaultConfig("127.0.0.1", 1)
	cfg.RequireTLS = false
	cfg.Timeout = 300 * time.Millisecond

	s := sender.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()

	err := s.Send(ctx, plainMessage())

	elapsed := time.Since(start)

	t.Logf("a send to a dead port returned after %v: %v", elapsed.Round(10*time.Millisecond), err)

	if err == nil {
		t.Fatal("the send succeeded")
	}

	// Temporary, because the connection never happened so the server never said anything.
	if !errors.Is(err, sender.ErrTemporary) {
		t.Errorf("got %v, want ErrTemporary", err)
	}

	if elapsed > 2*time.Second {
		t.Errorf("took %v with a 300ms timeout", elapsed)
	}

	// And a cancelled context stops the dial.
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()

	start = time.Now()

	err = s.Send(cancelled, plainMessage())

	t.Logf("with an already-cancelled context: %v after %v", err,
		time.Since(start).Round(time.Millisecond))

	if err == nil {
		t.Error("a cancelled context did not stop the send")
	}

	t.Log("net/smtp has no timeout at all: smtp.SendMail against a black hole hangs forever and " +
		"there is no option for it. The timeout has to come from a net.Dialer and a manually " +
		"driven Client, which is why Send is thirty lines rather than four.")
}

// TestTheRecorderTestsTheCaller, and what it cannot see.
func TestTheRecorderTestsTheCaller(t *testing.T) {
	rec := &sender.Recorder{}

	// A service that sends a welcome email, which is the code under test.
	notify := func(ctx context.Context, transport sender.Transport, name, email string) error {
		renderer, err := templates.New()
		if err != nil {
			return err
		}

		text, html, err := renderer.Welcome(templates.WelcomeData{
			Name:       name,
			Plan:       "Free",
			ConfirmURL: "https://example.test/confirm",
			Footer:     "Sent by Learn Go.",
		})
		if err != nil {
			return err
		}

		return transport.Send(ctx, &message.Message{
			From:    message.Address{Email: "noreply@example.test"},
			To:      []message.Address{{Name: name, Email: email}},
			Subject: "Welcome",
			Text:    text,
			HTML:    html,
		})
	}

	ctx := context.Background()

	if err := notify(ctx, rec, "Ada", "ada@example.test"); err != nil {
		t.Fatal(err)
	}

	sent := rec.Messages()

	if len(sent) != 1 {
		t.Fatalf("recorded %d messages", len(sent))
	}

	t.Logf("recorded: from=%s to=%v, %d bytes", sent[0].From, sent[0].Recipients, len(sent[0].Raw))

	if sent[0].Recipients[0] != "ada@example.test" {
		t.Errorf("sent to %v", sent[0].Recipients)
	}

	// A recorder can assert the message CONTAINS something, which is enough to catch a template
	// filled with the wrong data.
	if !strings.Contains(string(sent[0].Raw), "Welcome") {
		t.Error("the subject is missing")
	}

	// And the error path, which is the other half of what a fake is for.
	rec.Reset()
	rec.Err = errors.New("the SMTP server is down")

	err := notify(ctx, rec, "Ada", "ada@example.test")

	t.Logf("with a failing transport: %v", err)

	if err == nil {
		t.Error("the caller ignored a transport failure")
	}

	if len(rec.Messages()) != 0 {
		t.Error("a failed send was recorded")
	}

	t.Log("a recorder tests the CALLER: the right addresses, the right template data, the right " +
		"error handling. It cannot test the MESSAGE, because nothing parses it, which is why " +
		"this package has both a recorder and a Mailpit test.")
}

// TestErrorClassification, because retrying a 5xx is how you get blocked.
func TestErrorClassification(t *testing.T) {
	// A real 5xx: Mailpit rejects a recipient whose domain is deliberately malformed at the SMTP
	// level. Rather than depend on that, this asserts on the sentinel structure that a caller uses.
	t.Log("the classification rule, which the code applies from the reply's first digit:")
	t.Log("  2xx  success")
	t.Log("  4xx  ErrTemporary: try again later")
	t.Log("  5xx  ErrPermanent: do not try again")

	// The sentinels are distinct, which is what lets a retry loop switch on them.
	if errors.Is(sender.ErrTemporary, sender.ErrPermanent) {
		t.Error("the two sentinels match each other, so a caller cannot distinguish them")
	}

	// A dead port is temporary: the server never replied, so there is nothing permanent about it.
	cfg := sender.DefaultConfig("127.0.0.1", 1)
	cfg.RequireTLS = false
	cfg.Timeout = 200 * time.Millisecond

	err := sender.New(cfg).Send(context.Background(), plainMessage())

	if !errors.Is(err, sender.ErrTemporary) {
		t.Errorf("a refused connection is %v, want ErrTemporary", err)
	}
	if errors.Is(err, sender.ErrPermanent) {
		t.Error("a refused connection was classified as permanent")
	}

	t.Log("a sender that retries a 5xx is a sender that mail providers start rejecting, and one " +
		"that gives up on a 4xx loses mail that would have been delivered. The first digit is " +
		"the whole rule and net/smtp gives it to you in a *textproto.Error.")
}

// TestValidationHappensFirst, so a malformed message costs nothing.
func TestValidationHappensFirst(t *testing.T) {
	// A sender pointed at nothing, so a connection attempt would take the full timeout.
	cfg := sender.DefaultConfig("127.0.0.1", 1)
	cfg.RequireTLS = false
	cfg.Timeout = 5 * time.Second

	s := sender.New(cfg)

	bad := &message.Message{
		From:    message.Address{Email: "noreply@example.test"},
		To:      []message.Address{{Email: "ada@example.test"}},
		Subject: "Hello\r\nBcc: everyone@example.test",
		Text:    "hi",
	}

	start := time.Now()

	err := s.Send(context.Background(), bad)

	elapsed := time.Since(start)

	t.Logf("a message with an injected header failed after %v: %v",
		elapsed.Round(time.Millisecond), err)

	if !errors.Is(err, message.ErrHeaderInject) {
		t.Errorf("got %v, want ErrHeaderInject", err)
	}

	// It did not dial: the whole point of validating first.
	if elapsed > time.Second {
		t.Errorf("took %v, so it tried to connect before validating", elapsed)
	}

	sent, failed := s.Stats()

	t.Logf("stats: %d sent, %d failed", sent, failed)

	if failed != 1 {
		t.Errorf("failed=%d, want 1", failed)
	}
}

func plainMessage() *message.Message {
	return &message.Message{
		From:    message.Address{Email: "noreply@example.test"},
		To:      []message.Address{{Email: "ada@example.test"}},
		Subject: "Hello",
		Text:    "Hello.",
	}
}
