package message_test

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/email-concepts/message"
)

var fixedTime = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

func plainMessage() *message.Message {
	return &message.Message{
		From:    message.Address{Name: "Learn Go", Email: "noreply@example.test"},
		To:      []message.Address{{Name: "Ada Lovelace", Email: "ada@example.test"}},
		Subject: "Welcome",
		Text:    "Hello Ada,\n\nYour account is ready.\n",
		Date:    fixedTime,
	}
}

// TestHeaderInjectionIsRejected is the security one.
func TestHeaderInjectionIsRejected(t *testing.T) {
	// The attack: a contact form puts a user's name in a header.
	for _, tc := range []struct {
		name string
		mut  func(*message.Message)
	}{
		{"subject", func(m *message.Message) {
			m.Subject = "Hello\r\nBcc: everyone@example.test"
		}},
		{"subject with a bare LF", func(m *message.Message) {
			m.Subject = "Hello\nBcc: everyone@example.test"
		}},
		{"from name", func(m *message.Message) {
			m.From.Name = "Learn Go\r\nBcc: everyone@example.test"
		}},
		{"recipient name", func(m *message.Message) {
			m.To[0].Name = "Ada\r\nBcc: everyone@example.test"
		}},
		{"a custom header", func(m *message.Message) {
			m.Headers = map[string]string{"X-Thing": "a\r\nBcc: everyone@example.test"}
		}},
		{"a header NAME", func(m *message.Message) {
			m.Headers = map[string]string{"X-Thing\r\nBcc": "everyone@example.test"}
		}},
		{"a NUL byte", func(m *message.Message) {
			m.Subject = "Hello\x00Bcc: everyone@example.test"
		}},
	} {
		m := plainMessage()
		tc.mut(m)

		_, err := m.Bytes()

		if !errors.Is(err, message.ErrHeaderInject) {
			t.Errorf("%s: got %v, want ErrHeaderInject", tc.name, err)
			continue
		}

		t.Logf("%-24s rejected: %v", tc.name, err)
	}

	t.Log("this is how a contact form becomes an open relay: an attacker puts a newline and a Bcc " +
		"in the name field. Rejecting the message is the only safe response, because silently " +
		"stripping the newline sends a message the caller did not write.")

	// And the NUL case matters for a different reason: some implementations truncate at it, so a
	// value that looks harmless after the NUL is dropped can be the payload.
	t.Log("the NUL is rejected too, because an implementation that truncates at it turns " +
		`"harmless\x00<payload>" into something that passed a check on the whole string`)
}

// TestBccIsNeverInTheHeaders is the data-breach-in-one-line test.
func TestBccIsNeverInTheHeaders(t *testing.T) {
	m := plainMessage()
	m.Cc = []message.Address{{Email: "cc@example.test"}}
	m.Bcc = []message.Address{
		{Email: "secret1@example.test"},
		{Email: "secret2@example.test"},
	}

	raw, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	text := string(raw)

	t.Logf("headers:\n%s", headersOf(text))

	// The Cc IS in the headers.
	if !strings.Contains(text, "cc@example.test") {
		t.Error("the Cc recipient is missing from the headers")
	}

	// The Bcc is NOT.
	for _, secret := range []string{"secret1@example.test", "secret2@example.test"} {
		if strings.Contains(text, secret) {
			t.Errorf("%s appears in the message, which sends the Bcc list to everyone", secret)
		}
	}

	if strings.Contains(strings.ToLower(text), "bcc:") {
		t.Error("the message has a Bcc header")
	}

	// And they ARE in the envelope, which is the only place they exist.
	recipients := m.Recipients()

	t.Logf("envelope recipients: %v", recipients)

	if len(recipients) != 4 {
		t.Errorf("got %d envelope recipients, want 4", len(recipients))
	}

	for _, want := range []string{"ada@example.test", "cc@example.test",
		"secret1@example.test", "secret2@example.test"} {
		found := false
		for _, got := range recipients {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is missing from the envelope", want)
		}
	}

	t.Log("Bcc lives in the SMTP envelope (the RCPT TO commands) and nowhere else. A message that " +
		"writes a Bcc header sends the list to every recipient, which is a data breach in one " +
		"line and is what happens when a builder treats Bcc like Cc.")
}

// TestHeaderEncoding, because a header is ASCII.
func TestHeaderEncoding(t *testing.T) {
	for _, tc := range []struct {
		subject  string
		encoded  bool
		contains string
	}{
		{"Welcome", false, "Subject: Welcome"},
		{"Your invoice is ready", false, "Subject: Your invoice"},
		{"Café", true, "=?utf-8?q?"},
		{"日本語の件名", true, "=?utf-8?"},
		{"Welcome 🎉", true, "=?utf-8?"},
	} {
		m := plainMessage()
		m.Subject = tc.subject

		raw, err := m.Bytes()
		if err != nil {
			t.Fatal(err)
		}

		text := string(raw)

		if !strings.Contains(text, tc.contains) {
			t.Errorf("%q: the header does not contain %q", tc.subject, tc.contains)
		}

		// And it round-trips: a client decoding it gets the original back.
		parsed, err := mail.ReadMessage(strings.NewReader(text))
		if err != nil {
			t.Fatalf("%q: the message does not parse: %v", tc.subject, err)
		}

		decoded, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
		if err != nil {
			t.Fatalf("%q: decoding the subject: %v", tc.subject, err)
		}

		if decoded != tc.subject {
			t.Errorf("%q round-tripped as %q", tc.subject, decoded)
		}

		encoded := strings.Contains(text, "=?utf-8?")

		t.Logf("%-20q encoded=%-5v round-trips", tc.subject, encoded)
	}

	t.Log("an unencoded non-ASCII subject arrives as mojibake or is rejected. mime.QEncoding " +
		"leaves ASCII alone and encodes the rest, which is why this is one line rather than a " +
		"decision, and why hand-built email gets it wrong.")
}

// TestEverythingIsCRLF, because SMTP requires it.
func TestEverythingIsCRLF(t *testing.T) {
	m := plainMessage()

	// A body with every kind of line ending, which is what pasted content looks like.
	m.Text = "line one\nline two\r\nline three\rline four"
	m.HTML = "<p>one</p>\n<p>two</p>\r\n<p>three</p>"

	raw, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	text := string(raw)

	// No bare LF anywhere: every \n is preceded by \r.
	for i, b := range []byte(text) {
		if b == '\n' && (i == 0 || text[i-1] != '\r') {
			t.Fatalf("a bare LF at byte %d: %q", i, snippet(text, i))
		}

		if b == '\r' && (i+1 >= len(text) || text[i+1] != '\n') {
			t.Fatalf("a bare CR at byte %d: %q", i, snippet(text, i))
		}
	}

	t.Logf("%d bytes, every line ending is CRLF", len(text))

	t.Log("RFC 5322 requires CRLF and SMTP's DATA phase terminates on a lone dot on a " +
		"CRLF-terminated line. A message built with \\n is accepted by a permissive server and " +
		"mangled by a strict one, and the dot-stuffing rule can truncate the body.")
}

// TestPlainTextComesFirst is the ordering rule.
func TestPlainTextComesFirst(t *testing.T) {
	m := plainMessage()
	m.HTML = "<p>Hello Ada,</p>"
	m.SetBoundary("fixed")

	raw, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	text := string(raw)

	t.Logf("%s", indent(text))

	if !strings.Contains(text, "multipart/alternative") {
		t.Fatal("a message with both bodies is not multipart/alternative")
	}

	plainAt := strings.Index(text, "text/plain")
	htmlAt := strings.Index(text, "text/html")

	if plainAt < 0 || htmlAt < 0 {
		t.Fatalf("missing a part: plain at %d, html at %d", plainAt, htmlAt)
	}

	if plainAt > htmlAt {
		t.Error("the HTML part comes first, so every client shows the plain text")
	}

	t.Logf("text/plain at byte %d, text/html at %d", plainAt, htmlAt)

	t.Log("in multipart/alternative the LAST part is the PREFERRED one. Putting HTML first shows " +
		"the plain text in every client that honours the spec, which is all of them, and the " +
		"message looks like the HTML was never written.")
}

// TestNestingWithAttachments, because a client picks one part of an alternative and shows every part of a mixed.
func TestNestingWithAttachments(t *testing.T) {
	m := plainMessage()
	m.HTML = "<p>Hello Ada,</p>"
	m.Attachments = []message.Attachment{{
		Filename:    "invoice.pdf",
		ContentType: "application/pdf",
		Content:     []byte("%PDF-1.4 not really a pdf"),
	}}
	m.SetBoundary("fixed")

	raw, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	text := string(raw)

	t.Logf("%s", indent(truncate(text, 1200)))

	// The OUTER type is mixed and the alternative is nested inside it.
	mixedAt := strings.Index(text, "multipart/mixed")
	altAt := strings.Index(text, "multipart/alternative")

	if mixedAt < 0 || altAt < 0 {
		t.Fatalf("missing a level: mixed at %d, alternative at %d", mixedAt, altAt)
	}

	if mixedAt > altAt {
		t.Error("the alternative is outside the mixed, so a client preferring HTML never sees " +
			"the attachment")
	}

	// And the whole thing parses, with the boundaries matching.
	parsed, err := mail.ReadMessage(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}

	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}

	if mediaType != "multipart/mixed" {
		t.Errorf("the top-level type is %s", mediaType)
	}

	reader := multipart.NewReader(parsed.Body, params["boundary"])

	var parts []string

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading a part: %v", err)
		}

		parts = append(parts, part.Header.Get("Content-Type"))

		_, _ = io.Copy(io.Discard, part)
	}

	t.Logf("the mixed has %d part(s): %v", len(parts), parts)

	if len(parts) != 2 {
		t.Errorf("got %d parts, want 2 (the alternative and the attachment)", len(parts))
	}

	t.Log("the nesting is not decoration: a client picks ONE part of an alternative and shows " +
		"EVERY part of a mixed, so an attachment inside the alternative is invisible to a " +
		"client that prefers HTML")
}

// TestAttachmentEncoding, and the line wrapping.
func TestAttachmentEncoding(t *testing.T) {
	// A long body, so the base64 has to wrap.
	content := bytes.Repeat([]byte("binary data "), 200)

	m := plainMessage()
	m.Attachments = []message.Attachment{
		{Filename: "invoice.pdf", ContentType: "application/pdf", Content: content},
		// A non-ASCII filename, which needs RFC 2231.
		{Filename: "facture café.pdf", Content: []byte("small")},
		// No content type, so it is guessed from the extension.
		{Filename: "report.csv", Content: []byte("a,b,c")},
	}
	m.SetBoundary("fixed")

	raw, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	text := string(raw)

	// Every line is within the limit. RFC 5322 caps a line at 998 characters and recommends 78, and
	// base64.Encoder emits one endless line without a wrapper.
	longest := 0

	for _, line := range strings.Split(text, "\r\n") {
		if len(line) > longest {
			longest = len(line)
		}
	}

	t.Logf("%d bytes, longest line %d characters", len(text), longest)

	if longest > 998 {
		t.Errorf("a line is %d characters, over the RFC 5322 limit of 998", longest)
	}

	// The guessed content type.
	if !strings.Contains(text, "text/csv") {
		t.Error("the .csv attachment was not guessed as text/csv")
	}

	// The non-ASCII filename, encoded so it round-trips.
	parsed, err := mail.ReadMessage(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}

	_, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}

	reader := multipart.NewReader(parsed.Body, params["boundary"])

	var filenames []string

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading a part: %v", err)
		}

		if name := part.FileName(); name != "" {
			filenames = append(filenames, name)
		}

		_, _ = io.Copy(io.Discard, part)
	}

	t.Logf("filenames as a client sees them: %v", filenames)

	found := false
	for _, name := range filenames {
		if name == "facture café.pdf" {
			found = true
		}
	}

	if !found {
		t.Errorf("the non-ASCII filename did not round-trip: %v", filenames)
	}

	t.Log("mime.FormatMediaType handles RFC 2231 for a non-ASCII filename. Writing " +
		`filename="..." by hand works for ASCII and produces an unreadable name for anything ` +
		"else, which is the most common attachment bug there is.")
}

// TestInlineImages, and the cid asymmetry.
func TestInlineImages(t *testing.T) {
	m := plainMessage()
	m.HTML = `<p>Hello</p><img src="cid:logo" alt="logo">`
	m.Attachments = []message.Attachment{{
		Filename:    "logo.png",
		ContentType: "image/png",
		Content:     []byte("\x89PNG not really"),
		Inline:      true,
		ContentID:   "logo",
	}}
	m.SetBoundary("fixed")

	raw, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	text := string(raw)

	// The header name is Content-Id, not Content-ID.
	//
	// textproto.MIMEHeader.Set canonicalises the key: the first letter of each dash-separated word is
	// upper-cased and the rest lower-cased, so "Content-ID" becomes "Content-Id". That is correct
	// (header names are case-insensitive) and it means a test grepping for the spelling it wrote
	// fails, which is how this assertion failed first time.
	if !strings.Contains(text, "Content-Id: <logo>") {
		t.Errorf("no Content-Id header:\n%s", indent(truncate(text, 900)))
	}

	// The HTML reference is quoted-printable ENCODED, so `src="cid:logo"` appears as
	// `src=3D"cid:logo"`: quoted-printable encodes `=` as `=3D`, always, because `=` is its own
	// escape character.
	//
	// Which means grepping the raw message for HTML is wrong. The body has to be decoded first, and
	// that is what a mail client does.
	if strings.Contains(text, `src="cid:logo"`) {
		t.Error("the HTML reference is NOT encoded, so quoted-printable is not being applied")
	}

	if !strings.Contains(text, `src=3D"cid:logo"`) {
		t.Errorf("the encoded HTML reference is missing:\n%s", indent(truncate(text, 900)))
	}

	t.Log("quoted-printable encodes = as =3D, so every HTML attribute in the raw message reads " +
		`src=3D"...". Searching the raw bytes for HTML finds nothing, which looks like the ` +
		"body was never written.")

	if !strings.Contains(text, "Content-Disposition: inline") {
		t.Error("the part is not inline")
	}

	t.Log(`Content-Id: <logo> in the header, src="cid:logo" in the HTML. The angle brackets are ` +
		"required in one and forbidden in the other, and getting that asymmetry wrong shows a " +
		"broken image with nothing to explain why.")

	t.Log("and the strictly correct nesting for an inline image is multipart/related wrapping the " +
		"alternative, not multipart/mixed. This builder uses mixed with " +
		"Content-Disposition: inline, which every client I know of honours; a client that " +
		"follows RFC 2387 to the letter would be within its rights to show the image as an " +
		"attachment.")

	// An inline attachment with no Content-ID is an error rather than a silently broken image.
	m.Attachments[0].ContentID = ""

	if _, err := m.Bytes(); err == nil {
		t.Error("an inline attachment with no Content-ID was accepted")
	} else {
		t.Logf("without a Content-ID: %v", err)
	}
}

// TestValidation.
func TestValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*message.Message)
		want error
	}{
		{"no sender", func(m *message.Message) { m.From = message.Address{} }, message.ErrNoSender},
		{"no recipient", func(m *message.Message) { m.To = nil }, message.ErrNoRecipient},
		{"no body", func(m *message.Message) { m.Text = ""; m.HTML = "" }, message.ErrNoBody},
	} {
		m := plainMessage()
		tc.mut(m)

		_, err := m.Bytes()

		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		} else {
			t.Logf("%-14s %v", tc.name, err)
		}
	}

	// An unparseable recipient is caught before sending, rather than bouncing later.
	m := plainMessage()
	m.To = []message.Address{{Email: "not an address"}}

	if _, err := m.Bytes(); err == nil {
		t.Error("an unparseable recipient was accepted")
	} else {
		t.Logf("unparseable recipient: %v", err)
	}

	t.Log("an unparseable address accepted here is rejected by the server after the queue has " +
		"taken the message, which is a bounce rather than an error the caller sees")
}

// TestTheWholeMessageParses is the check that everything above has not produced something only Go can read.
func TestTheWholeMessageParses(t *testing.T) {
	m := &message.Message{
		From:    message.Address{Name: "Learn Go", Email: "noreply@example.test"},
		To:      []message.Address{{Name: "Ada Lovelace", Email: "ada@example.test"}},
		Cc:      []message.Address{{Email: "cc@example.test"}},
		ReplyTo: &message.Address{Email: "support@example.test"},
		Subject: "Café ☕ and a very long subject line that will need folding because it is well over seventy-eight characters",
		Text:    "Hello Ada,\n\nYour café order is ready.\n",
		HTML:    "<p>Hello Ada,</p><p>Your café order is ready.</p>",
		Headers: map[string]string{
			"List-Unsubscribe": "<https://example.test/unsubscribe?t=abc>",
			"Message-ID":       "<abc123@example.test>",
		},
		Attachments: []message.Attachment{
			{Filename: "receipt.pdf", ContentType: "application/pdf",
				Content: bytes.Repeat([]byte("pdf"), 500)},
		},
		Date: fixedTime,
	}

	raw, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the message does not parse: %v", err)
	}

	decoder := new(mime.WordDecoder)

	subject, err := decoder.DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("subject decodes to %q", subject)

	if subject != m.Subject {
		t.Errorf("the subject round-tripped as %q", subject)
	}

	// Addresses parse.
	to, err := parsed.Header.AddressList("To")
	if err != nil {
		t.Fatal(err)
	}

	if len(to) != 1 || to[0].Address != "ada@example.test" || to[0].Name != "Ada Lovelace" {
		t.Errorf("To parsed as %+v", to)
	}

	if got := parsed.Header.Get("List-Unsubscribe"); got == "" {
		t.Error("the custom header is missing")
	}

	t.Logf("%d bytes, parses with net/mail, headers decode, addresses parse", len(raw))
}

func headersOf(s string) string {
	if i := strings.Index(s, "\r\n\r\n"); i >= 0 {
		return indent(s[:i])
	}
	return indent(s)
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\n  ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (" + itoa(len(s)) + " bytes total)"
}

func snippet(s string, at int) string {
	start := max(at-20, 0)
	end := min(at+20, len(s))

	return s[start:end]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
