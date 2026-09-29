// Package message builds MIME email, which is harder than it looks and is entirely stdlib.
//
// # What Go gives you and what it does not
//
// net/smtp sends. mime/multipart builds a multipart body. mime.QEncoding and mime.BEncoding encode headers.
// net/mail parses addresses and messages. html/template renders the body.
//
// What NOTHING in the standard library does is assemble them: there is no `mail.Message.Write`, no attachment
// helper, and no builder. So every Go project either writes this file or imports one of half a dozen libraries
// that have. Writing it out is 300 lines and every line is a decision the libraries make for you.
//
// # Why the details matter
//
// An email that is subtly malformed does not bounce. It arrives with the subject rendered as
// `=?utf-8?q?...`, or with the attachment inline, or with the HTML shown as source, or it goes to spam. There
// is no status code and no error, which is what makes email different from HTTP.
//
// # The four that bite
//
//	HEADER ENCODING   a header is ASCII. A subject with an accent, an emoji or a non-Latin script
//	                  has to be RFC 2047 encoded, and Go will not do it for you.
//	LINE ENDINGS      SMTP requires CRLF. A message built with \n is accepted by a permissive
//	                  server and rejected or mangled by a strict one.
//	HEADER INJECTION  a newline in a user-supplied name or subject inserts a header. That is how
//	                  a contact form becomes an open relay.
//	MULTIPART ORDER   in multipart/alternative the LAST part is the preferred one, so putting HTML
//	                  first shows the plain text.
package message

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Errors a caller distinguishes.
var (
	ErrNoRecipient  = errors.New("no recipient")
	ErrNoSender     = errors.New("no sender")
	ErrHeaderInject = errors.New("header injection attempt")
	ErrNoBody       = errors.New("no body")

	// ErrReservedHeader means Headers tried to set a header the message writes itself.
	ErrReservedHeader = errors.New("reserved header")
)

// Address is a name and an email address.
type Address struct {
	Name  string
	Email string
}

// String renders it for a header, encoding the name if it is not ASCII.
//
// mail.Address.String does the RFC 2047 encoding and the quoting, which is the one piece of this the standard
// library does provide and which most hand-rolled email code reimplements badly.
func (a Address) String() string {
	return (&mail.Address{Name: a.Name, Address: a.Email}).String()
}

// Attachment is a file to attach.
type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte

	// Inline makes it a related part referenced by a cid: URL from the HTML, rather than a
	// downloadable attachment. The difference is Content-Disposition, and getting it wrong is why
	// a logo appears as an attachment or an invoice appears inline.
	Inline bool

	// ContentID is the cid: target for an inline part. Required when Inline is set.
	ContentID string
}

// Message is an email being built.
type Message struct {
	From    Address
	To      []Address
	Cc      []Address
	Bcc     []Address
	ReplyTo *Address

	Subject string

	// Text and HTML. Both is the normal case and produces multipart/alternative.
	Text string
	HTML string

	Attachments []Attachment

	// Headers are extra headers. Used for List-Unsubscribe, Message-ID, In-Reply-To and anything
	// else a transactional sender needs.
	Headers map[string]string

	// Date defaults to now. Injectable so a test can produce a byte-identical message.
	Date time.Time

	// boundary is fixed for a test and random otherwise. multipart.Writer generates a random one,
	// which makes a golden test impossible, so SetBoundary exists.
	boundary string
}

// SetBoundary fixes the MIME boundary, for a test that compares bytes.
//
// Never call this in production: a boundary that appears in the body breaks the message, and the whole point of
// a random one is that it cannot.
func (m *Message) SetBoundary(b string) { m.boundary = b }

// Recipients returns every address the message is addressed to, Bcc included.
//
// # The Bcc rule
//
// Bcc recipients go in the SMTP envelope (the RCPT TO commands) and NOT in the headers. That is the whole of
// what Bcc means: the header is absent, so no recipient can see the list.
//
// A message that writes a Bcc header sends the list to everyone, which is a data breach in one line. This
// function is what the sender uses for RCPT TO, and Bytes() below never writes the header.
func (m *Message) Recipients() []string {
	out := make([]string, 0, len(m.To)+len(m.Cc)+len(m.Bcc))

	for _, group := range [][]Address{m.To, m.Cc, m.Bcc} {
		for _, a := range group {
			out = append(out, a.Email)
		}
	}

	return out
}

// Validate checks what has to be true before sending.
func (m *Message) Validate() error {
	if m.From.Email == "" {
		return ErrNoSender
	}

	if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
		return ErrNoRecipient
	}

	if m.Text == "" && m.HTML == "" && len(m.Attachments) == 0 {
		return ErrNoBody
	}

	// Header injection, checked on everything that reaches a header. A newline in a subject or a
	// display name inserts a header, and that is how a contact form becomes an open relay: an
	// attacker puts `\r\nBcc: everyone@example.com` in the name field.
	//
	// The check is here rather than at the write, because rejecting the message is the only safe
	// response: silently stripping the newline sends a message the caller did not write.
	for _, field := range []struct{ name, value string }{
		{"subject", m.Subject},
		{"from name", m.From.Name},
		{"from address", m.From.Email},
	} {
		if err := checkHeaderValue(field.name, field.value); err != nil {
			return err
		}
	}

	for _, group := range [][]Address{m.To, m.Cc, m.Bcc} {
		for _, a := range group {
			if err := checkHeaderValue("recipient name", a.Name); err != nil {
				return err
			}
			if err := checkHeaderValue("recipient address", a.Email); err != nil {
				return err
			}

			// A recipient address has to parse, because an unparseable one is rejected by the
			// server after the message has been accepted by the queue, which is a bounce
			// rather than an error the caller sees.
			if _, err := mail.ParseAddress(a.Email); err != nil {
				return fmt.Errorf("recipient %q: %w", a.Email, err)
			}
		}
	}

	for name, value := range m.Headers {
		if err := checkHeaderValue(name, value); err != nil {
			return err
		}
		if err := checkHeaderValue("header name", name); err != nil {
			return err
		}
		if name == "" || strings.ContainsAny(name, ": \t") {
			return fmt.Errorf("%w: %q is not a header name", ErrHeaderInject, name)
		}

		// The headers this type writes itself cannot come in through Headers. Without this check,
		// Headers{"Bcc": ...} wrote a Bcc header into a message whose whole design is that Bcc
		// never appears in it, and Headers{"From": ...} produced a second From.
		if reservedHeaders[textproto.CanonicalMIMEHeaderKey(name)] {
			return fmt.Errorf("%w: %s is set by the message, not through Headers", ErrReservedHeader, name)
		}
	}

	return nil
}

// reservedHeaders are the ones Bytes writes from the Message's own fields, plus Bcc, which it never writes.
var reservedHeaders = map[string]bool{
	"Bcc": true, "Cc": true, "To": true, "From": true, "Reply-To": true, "Subject": true, "Date": true,
	"Mime-Version": true, "Content-Type": true, "Content-Transfer-Encoding": true,
}

// checkHeaderValue rejects anything that could inject a header.
//
// CR and LF are the injection. A NUL is rejected too, because it truncates the value in some implementations,
// which is a different bug with the same shape.
func checkHeaderValue(field, value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%w: %s contains a control character", ErrHeaderInject, field)
	}

	return nil
}

// Bytes renders the message.
//
// # CRLF everywhere
//
// RFC 5322 says lines end with CRLF and SMTP's data phase uses a lone "." on a CRLF-terminated line as the
// terminator. A message built with "\n" is accepted by a permissive server, mangled by a strict one, and breaks
// the dot-stuffing rule in a way that can truncate the body.
//
// Go's mime/multipart writes CRLF, and every header written here does too. The one place to get it wrong is a
// body that contains bare newlines, which is why Text and HTML are normalised.
func (m *Message) Bytes() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	var buf bytes.Buffer

	date := m.Date
	if date.IsZero() {
		date = time.Now()
	}

	headers := []struct{ name, value string }{
		{"From", m.From.String()},
		{"To", joinAddresses(m.To)},
		{"Cc", joinAddresses(m.Cc)},
		{"Reply-To", replyTo(m.ReplyTo)},
		{"Subject", encodeHeader(m.Subject)},
		{"Date", date.Format(time.RFC1123Z)},
		{"MIME-Version", "1.0"},
	}

	for _, h := range headers {
		if h.value == "" {
			continue
		}

		// No Bcc header. Ever. See Recipients.
		writeHeader(&buf, h.name, h.value)
	}

	// Sorted, so a message renders the same bytes every time; ranging over the map, as the first
	// version did, put them in a different order on every call and broke SetBoundary's promise of a
	// byte-identical message. And encoded, like Subject, so a non-ASCII value is legal on the wire.
	for _, name := range slices.Sorted(maps.Keys(m.Headers)) {
		writeHeader(&buf, name, encodeHeader(m.Headers[name]))
	}

	if err := m.writeBody(&buf); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// writeBody writes the MIME structure.
//
// # The three shapes, and why they nest
//
//	text only             Content-Type: text/plain
//	text + html           multipart/alternative
//	the above + files     multipart/mixed wrapping the alternative
//
// An inline image strictly belongs in a multipart/related wrapping the alternative, per RFC 2387. This builder
// puts it in the mixed with Content-Disposition: inline and a Content-ID, which every client I know of honours
// and which a client following the RFC to the letter would be within its rights to show as an attachment.
//
// Adding the related level is another twenty lines and another boundary, and saying so is better than
// implementing four levels of nesting in a package about the first three.
//
// The nesting is not decoration. A client picks ONE part of an alternative and shows every part of a mixed, so
// putting an attachment inside the alternative means a client that prefers HTML never sees the attachment.
func (m *Message) writeBody(buf *bytes.Buffer) error {
	hasAlternative := m.Text != "" && m.HTML != ""
	hasAttachments := len(m.Attachments) > 0

	switch {
	case !hasAlternative && !hasAttachments:
		return m.writeSinglePart(buf)

	case hasAttachments:
		return m.writeMixed(buf)

	default:
		return m.writeAlternative(buf)
	}
}

func (m *Message) writeSinglePart(buf *bytes.Buffer) error {
	contentType := "text/plain; charset=utf-8"
	body := m.Text

	if m.HTML != "" {
		contentType = "text/html; charset=utf-8"
		body = m.HTML
	}

	writeHeader(buf, "Content-Type", contentType)

	// quoted-printable, not 8bit. A body with a non-ASCII character in 8bit is rejected by any
	// server that has not advertised the 8BITMIME extension, and quoted-printable is safe
	// everywhere at the cost of being unreadable in a raw message.
	writeHeader(buf, "Content-Transfer-Encoding", "quoted-printable")

	buf.WriteString("\r\n")

	return writeQuotedPrintable(buf, body)
}

// buildAlternative renders a multipart/alternative body and returns it with its boundary.
//
// Returning the bytes and the boundary rather than writing straight into the output is what makes the nested
// case work: a multipart/mixed needs the alternative's boundary in a part HEADER, which has to be written before
// the part's content, so the content has to exist first.
//
// The first version of this wrote the header from one multipart.Writer and the body from another, which
// generated two different random boundaries and produced a message every client shows as empty.
func (m *Message) buildAlternative() ([]byte, string, error) {
	var buf bytes.Buffer

	w := multipart.NewWriter(&buf)

	if m.boundary != "" {
		if err := w.SetBoundary(m.boundary + "-alt"); err != nil {
			return nil, "", fmt.Errorf("setting the alternative boundary: %w", err)
		}
	}

	// PLAIN TEXT FIRST. In multipart/alternative the LAST part is the preferred one, so HTML goes
	// second. Reversing them shows the plain text in every client that honours the spec, which is
	// all of them, and the message looks like the HTML was never written.
	if m.Text != "" {
		if err := writePart(w, "text/plain; charset=utf-8", m.Text); err != nil {
			return nil, "", err
		}
	}

	if m.HTML != "" {
		if err := writePart(w, "text/html; charset=utf-8", m.HTML); err != nil {
			return nil, "", err
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("closing the alternative: %w", err)
	}

	return buf.Bytes(), w.Boundary(), nil
}

// writeAlternative writes a top-level multipart/alternative.
func (m *Message) writeAlternative(buf *bytes.Buffer) error {
	body, boundary, err := m.buildAlternative()
	if err != nil {
		return err
	}

	writeHeader(buf, "Content-Type", "multipart/alternative; boundary="+quoteBoundary(boundary))
	buf.WriteString("\r\n")

	_, err = buf.Write(body)

	return err
}

// writeMixed wraps everything in a multipart/mixed.
//
// The nesting matters: a client picks ONE part of an alternative and shows EVERY part of a mixed. So an
// attachment inside the alternative is invisible to a client that prefers HTML, and the alternative has to be
// one part of the mixed rather than the other way round.
func (m *Message) writeMixed(buf *bytes.Buffer) error {
	w := multipart.NewWriter(buf)

	if m.boundary != "" {
		if err := w.SetBoundary(m.boundary + "-mix"); err != nil {
			return fmt.Errorf("setting the mixed boundary: %w", err)
		}
	}

	writeHeader(buf, "Content-Type", "multipart/mixed; boundary="+quoteBoundary(w.Boundary()))
	buf.WriteString("\r\n")

	switch {
	case m.Text != "" && m.HTML != "":
		body, boundary, err := m.buildAlternative()
		if err != nil {
			return err
		}

		header := textproto.MIMEHeader{}
		header.Set("Content-Type", "multipart/alternative; boundary="+quoteBoundary(boundary))

		part, err := w.CreatePart(header)
		if err != nil {
			return fmt.Errorf("creating the alternative part: %w", err)
		}

		if _, err := part.Write(body); err != nil {
			return fmt.Errorf("writing the alternative part: %w", err)
		}

	case m.Text != "":
		if err := writePart(w, "text/plain; charset=utf-8", m.Text); err != nil {
			return err
		}

	case m.HTML != "":
		if err := writePart(w, "text/html; charset=utf-8", m.HTML); err != nil {
			return err
		}
	}

	for _, a := range m.Attachments {
		if err := writeAttachment(w, a); err != nil {
			return err
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("closing the mixed: %w", err)
	}

	return nil
}

func writePart(w *multipart.Writer, contentType, body string) error {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "quoted-printable")

	part, err := w.CreatePart(header)
	if err != nil {
		return fmt.Errorf("creating a %s part: %w", contentType, err)
	}

	var buf bytes.Buffer

	if err := writeQuotedPrintable(&buf, body); err != nil {
		return err
	}

	if _, err := part.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("writing a %s part: %w", contentType, err)
	}

	return nil
}

func writeAttachment(w *multipart.Writer, a Attachment) error {
	contentType := a.ContentType
	if contentType == "" {
		// Guessed from the extension, and falling back to application/octet-stream rather than
		// text/plain. A guess that says "binary" is always safe; one that says "text" makes a
		// client render a PDF as mojibake.
		contentType = mime.TypeByExtension(filepath.Ext(a.Filename))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
	}

	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)

	// base64, always, for an attachment. quoted-printable works for text and expands binary by
	// about 3x; base64 expands by 4/3 whatever the content.
	header.Set("Content-Transfer-Encoding", "base64")

	// The filename is encoded with mime.FormatMediaType, which handles RFC 2231 for a non-ASCII
	// name. Writing `filename="..."` by hand works for ASCII and produces an unreadable name for
	// anything else, which is the most common attachment bug there is.
	disposition := "attachment"
	if a.Inline {
		disposition = "inline"
	}

	header.Set("Content-Disposition",
		mime.FormatMediaType(disposition, map[string]string{"filename": a.Filename}))

	if a.Inline {
		if a.ContentID == "" {
			return fmt.Errorf("inline attachment %q has no Content-ID", a.Filename)
		}

		// The angle brackets are required, and the HTML references it WITHOUT them:
		// `<img src="cid:logo">` against `Content-ID: <logo>`. Getting that asymmetry wrong
		// shows a broken image and nothing explains why.
		header.Set("Content-ID", "<"+a.ContentID+">")
	}

	part, err := w.CreatePart(header)
	if err != nil {
		return fmt.Errorf("creating the attachment part for %q: %w", a.Filename, err)
	}

	encoder := base64.NewEncoder(base64.StdEncoding, &lineWrapper{w: part, limit: 76})

	if _, err := encoder.Write(a.Content); err != nil {
		return fmt.Errorf("encoding %q: %w", a.Filename, err)
	}

	if err := encoder.Close(); err != nil {
		return fmt.Errorf("closing the encoder for %q: %w", a.Filename, err)
	}

	return nil
}

// lineWrapper breaks base64 output into lines.
//
// RFC 5322 caps a line at 998 characters and recommends 78. base64.Encoder emits one endless line, so an
// attachment of any size produces a message that a strict server rejects and a lenient one accepts. 76 is the
// conventional width, chosen because 76 base64 characters is 57 bytes and everything used to be a multiple of
// something.
type lineWrapper struct {
	w     io.Writer
	limit int
	count int
}

func (l *lineWrapper) Write(p []byte) (int, error) {
	written := 0

	for len(p) > 0 {
		room := l.limit - l.count

		n := min(room, len(p))

		if _, err := l.w.Write(p[:n]); err != nil {
			return written, err
		}

		written += n
		l.count += n
		p = p[n:]

		if l.count >= l.limit {
			if _, err := l.w.Write([]byte("\r\n")); err != nil {
				return written, err
			}

			l.count = 0
		}
	}

	return written, nil
}

// encodeHeader RFC 2047 encodes a header value if it needs it.
//
// mime.QEncoding.Encode leaves ASCII alone and encodes anything else, which is what makes this one line rather
// than a decision. Q encoding rather than B: for a mostly-ASCII string with one accent it is far shorter and
// remains human-readable in a raw message.
func encodeHeader(value string) string {
	return mime.QEncoding.Encode("utf-8", value)
}

// writeHeader writes one header, folded.
//
// RFC 5322 says a line SHOULD be at most 78 characters and MUST be at most 998. A long header is FOLDED:
// broken at whitespace, with each continuation line starting with a space. The first version wrote every header
// on one line, so a long subject made a line over 998 that a strict server rejects. Encoded values fold too,
// because mime's Q encoding already splits a long value into space-separated encoded-words of at most 75.
func writeHeader(buf *bytes.Buffer, name, value string) {
	const limit = 78

	line := name + ":"

	for _, word := range strings.Fields(value) {
		if len(line)+1+len(word) > limit && len(line) > len(name)+1 {
			buf.WriteString(line)
			buf.WriteString("\r\n")
			line = ""
		}
		line += " " + word
	}

	buf.WriteString(line)
	buf.WriteString("\r\n")
}

func joinAddresses(addrs []Address) string {
	if len(addrs) == 0 {
		return ""
	}

	parts := make([]string, len(addrs))
	for i, a := range addrs {
		parts[i] = a.String()
	}

	return strings.Join(parts, ", ")
}

func replyTo(a *Address) string {
	if a == nil {
		return ""
	}
	return a.String()
}

// quoteBoundary quotes a boundary if it needs it.
//
// A boundary containing an `=` or other special character has to be quoted, and multipart.Writer's random
// boundaries do contain them. Writing `boundary=` + the raw value works most of the time and produces a message
// no client can parse the rest of the time.
func quoteBoundary(b string) string {
	if strings.ContainsAny(b, ` ()<>@,;:\"/[]?=`) {
		return `"` + b + `"`
	}
	return b
}

// writeQuotedPrintable encodes a body.
//
// # Why quoted-printable rather than 8bit or base64
//
// 8bit is rejected by any server that has not advertised the 8BITMIME extension, and a body with one accented
// character is 8bit. base64 works and makes the whole body unreadable in a raw message and in a mail client's
// "view source", which matters when debugging a template.
//
// quoted-printable encodes only the bytes that need it, so an English body is nearly plain text and an accent
// becomes =C3=A9. It also handles the line-length limit and the trailing-whitespace rule, both of which are
// silent corruption when done by hand.
//
// # The normalisation
//
// The body is normalised to CRLF first, and with this encoder that is belt and braces: quotedprintable.Writer
// in text mode (Binary false, the default) already turns \n, \r and \r\n into CRLF. An earlier version of this
// comment said it passes a lone \n through; checked, it does not. The normalisation stays because a body can
// reach an SMTP server by other paths than this encoder, and mixed line endings are rejected during the DATA
// phase with an error that names neither the line nor the reason.
func writeQuotedPrintable(w io.Writer, body string) error {
	encoder := quotedprintable.NewWriter(w)

	if _, err := encoder.Write([]byte(normaliseCRLF(body))); err != nil {
		return fmt.Errorf("encoding the body: %w", err)
	}

	if err := encoder.Close(); err != nil {
		return fmt.Errorf("closing the encoder: %w", err)
	}

	return nil
}

// normaliseCRLF turns any mixture of line endings into CRLF.
//
// The order matters: replacing \n with \r\n first would turn an existing \r\n into \r\r\n. So CRLF is
// collapsed to LF, then every LF becomes CRLF, and a lone CR is handled too because old Mac line endings still
// turn up in pasted content.
func normaliseCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	return strings.ReplaceAll(s, "\n", "\r\n")
}

// There is no randomBoundary here, and that is worth a note: multipart.Writer generates one, with 30 hex
// characters from crypto/rand, so hand-rolling one is duplicating the standard library. A boundary that appears
// in the body breaks the message, and 120 random bits makes that impossible rather than unlikely, which a
// counter or a timestamp would not.
//
// SetBoundary exists only so a test can produce byte-identical output. Calling it in production replaces a
// guarantee with a hope.
