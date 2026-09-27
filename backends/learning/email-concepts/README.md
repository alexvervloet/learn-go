# email-concepts

Building, sending and testing email with nothing but the standard library, and the reason every Go project either
writes this or imports a library that has. 2,915 lines.

The Python mirror is `backends/learning/email-concepts`, which uses `smtplib`, `email.message` and `imaplib`. The
Python standard library has a message BUILDER and Go does not, which is the difference that shapes this module.

## Running it

```sh
docker compose up -d
go test ./...
```

Without Mailpit the message and template tests still run, which is most of them. Only the tests that need a real
SMTP conversation skip. The whole suite is under a second.

Mailpit's UI is at http://localhost:8026, and looking at a message there while writing a template is the fastest
way to find a MIME mistake.

## What is here

| path | lines | what it is |
| --- | --- | --- |
| [message/](message/) | 1,290 | a MIME builder: headers, multipart, attachments, inline images |
| [sender/](sender/) | 800 | SMTP submission with TLS, timeouts and error classification |
| [templates/](templates/) | 456 | two template packages, embedded, rendered together |
| [mailpit/](mailpit/) | 326 | a client for the catcher, so a test can read what arrived |

Coverage: message 89.5%, templates 89.7%, sender 69.6%.

## What Go gives you and what it does not

`net/smtp` sends. `mime/multipart` builds a multipart body. `mime.QEncoding` encodes headers.
`mime/quotedprintable` encodes bodies. `net/mail` parses. `html/template` renders.

What nothing in the standard library does is **assemble them**. There is no `mail.Message.Write`, no attachment
helper and no builder. So `message/` is 400 lines of code that Python's `email.message.EmailMessage` is, and
every line is a decision a library would make for you.

`net/smtp` is also **frozen**: its documentation says it is "not accepting new features". It does PLAIN and
CRAM-MD5, and not XOAUTH2, which Gmail and Office 365 now require. A service sending through those needs a
third-party library or its own implementation.

## The four things that make email different from HTTP

**A malformed message does not bounce.** It arrives with the subject rendered as `=?utf-8?q?...`, or the
attachment inline, or the HTML shown as source, or it goes to spam. There is no status code and no error.

**A header is ASCII.** A subject with an accent, an emoji or a non-Latin script has to be RFC 2047 encoded, and
Go will not do it for you. Measured round trips through `net/mail`:

| subject | encoded | decodes back |
| --- | --- | --- |
| `Welcome` | no | yes |
| `Café` | `=?utf-8?q?Caf=C3=A9?=` | yes |
| `日本語の件名` | `=?utf-8?b?...` | yes |
| `Welcome 🎉` | `=?utf-8?q?Welcome_=F0=9F=8E=89?=` | yes |

**Lines end with CRLF.** RFC 5322 requires it and SMTP's DATA phase terminates on a lone dot on a
CRLF-terminated line. A message built with `\n` is accepted by a permissive server, mangled by a strict one, and
can be truncated by the dot-stuffing rule. Every line of every message this builder produces is CRLF, and the
test walks the bytes to prove it.

**A newline in a user-supplied value inserts a header.** That is how a contact form becomes an open relay: an
attacker puts `\r\nBcc: everyone@example.com` in the name field. Seven injection attempts are tested and all
seven are rejected, including one with a NUL byte, because an implementation that truncates at NUL turns
`harmless\x00<payload>` into something that passed a check on the whole string.

Rejecting the message is the only safe response. Silently stripping the newline sends a message the caller did
not write.

## Bcc lives in the envelope, and only there

This is a data breach in one line if you get it wrong.

| | headers | SMTP envelope (RCPT TO) |
| --- | --- | --- |
| To | yes | yes |
| Cc | yes | yes |
| **Bcc** | **never** | yes |

`Message.Recipients()` returns all three for the envelope, and `Message.Bytes()` never writes a Bcc header. The
test sends to a To, a Cc and two Bcc addresses and checks that neither secret address appears anywhere in the
message.

And a finding about the TOOL rather than the code: **Mailpit prepends a Bcc header to the raw message it
stores**, reconstructed from the envelope, along with `Message-ID`, `Return-Path` and `Received`. So an assertion
that the raw message has no Bcc header fails against Mailpit and the builder is right. The only place to answer
"what did we send" is the builder's output.

## MIME nesting

```
text only              text/plain
text + html            multipart/alternative
+ attachments          multipart/mixed
                         └── multipart/alternative
                         └── application/pdf
```

**Plain text comes FIRST in an alternative**, because the LAST part is the preferred one. Putting HTML first
shows the plain text in every client that honours the spec, which is all of them, and the message looks like the
HTML was never written.

**The attachment goes outside the alternative**, because a client picks ONE part of an alternative and shows
EVERY part of a mixed. An attachment inside the alternative is invisible to a client that prefers HTML.

An inline image strictly belongs in a `multipart/related` wrapping the alternative, per RFC 2387. This builder
uses the mixed with `Content-Disposition: inline` and a `Content-ID`, which every client I know of honours, and
says so rather than implementing a fourth level of nesting.

## Four encoding details, each measured

**`quoted-printable`, not 8bit.** A body with one accented character is 8bit, and 8bit is rejected by any server
that has not advertised the 8BITMIME extension. `base64` works and makes the body unreadable in a raw message,
which matters when debugging a template.

**`base64` for attachments, wrapped at 76 characters.** `base64.Encoder` emits one endless line, and RFC 5322
caps a line at 998. A 2.4 KB attachment produced a message whose longest line is **76 characters**; without the
wrapper it would be one line of 3,200.

**`mime.FormatMediaType` for the filename.** `facture café.pdf` round-trips through `multipart.Part.FileName()`.
Writing `filename="..."` by hand works for ASCII and produces an unreadable name for anything else, which is the
most common attachment bug there is.

**`Content-Id: <logo>` in the header and `src="cid:logo"` in the HTML.** The angle brackets are required in one
and forbidden in the other. Two things caught that test out:

- `textproto.MIMEHeader.Set` canonicalises the key, so `Content-ID` is stored as `Content-Id`. A test grepping
  for the spelling it wrote fails.
- quoted-printable encodes `=` as `=3D`, always, because `=` is its own escape character. So the raw message
  reads `src=3D"cid:logo"`, and grepping the raw bytes for HTML finds nothing.

## Sending

**`smtp.SendMail` has no timeout and no way to require TLS**, and those are the two things a transactional sender
needs most. It uses STARTTLS when the server advertises it and sends **in the clear** when it does not, silently.
So a server that stops advertising STARTTLS after a certificate expires starts sending every message and every
password in plaintext, and nothing reports it.

`RequireTLS` defaults to **true** for that reason, and the Mailpit test has to switch it off explicitly, which is
the right way round.

The timeout comes from a `net.Dialer` plus a deadline on the connection, because the dialler's timeout covers
only the dial: a server that accepts the connection and then says nothing would hang forever. A send to a dead
port with a 300ms timeout returns in **0ms** with `ErrTemporary`.

**Errors are classified by the reply's first digit:**

| | meaning | retry |
| --- | --- | --- |
| 2xx | accepted | n/a |
| 4xx | transient | yes |
| 5xx | permanent | **no** |

A sender that retries a 5xx is a sender that mail providers start rejecting. One that gives up on a 4xx loses
mail that would have been delivered. A transport failure is temporary by definition: the connection broke, so
the server never said anything.

And the error from `w.Close()` at the end of the DATA phase is the one that must not be ignored: that is where
the server accepts or rejects the message. A sender that ignores it reports success for a message the server
refused, with no bounce, no log and no email.

## Templates: two packages with the same API

`html/template` escapes **contextually**: a value in a text node is escaped for HTML, one in an attribute for an
attribute, one in a `<script>` for JavaScript. `text/template` escapes nothing.

Rendering the same template with each, given a name of `Ada <script>alert("xss")</script>`:

```
text/template:  <p>Hello Ada <script>alert("xss")</script></p>
html/template:  <p>Hello Ada &lt;script&gt;alert(&#34;xss&#34;)&lt;/script&gt;</p>
```

So rendering HTML with `text/template` compiles, runs, and is a cross-site scripting hole. The two packages have
the same API, so the compiler cannot help and the only defence is the import line. Getting it backwards the other
way turns an apostrophe into `&#39;` in an email nobody renders as HTML.

**A struct, not a map.** A template referencing `.Nmae` against a struct fails at Execute with
`can't evaluate field Nmae in type templates.WelcomeData`. The same template against a map renders
`Hello <no value>` with no error, which reaches production as an email with a blank where the name should be.

**Both bodies from one call**, because they have to agree. A service that renders them separately ends up with an
HTML body offering a discount and a plain-text body that does not, and nobody reads the plain-text one so nobody
notices.

**Embedded with `go:embed`**, so the binary has no runtime dependency on a directory. An email service that reads
its templates from disk fails in a container that did not copy them, at the moment it tries to send.

## Testing email: a recorder AND a real server

| | tests | cannot test |
| --- | --- | --- |
| `sender.Recorder` | the CALLER: right addresses, right template data, right error handling | whether the message is valid, because nothing parses it |
| Mailpit | the MESSAGE: it parses, parts in order, attachments decode | deliverability: SPF, DKIM, DMARC, reputation |

A suite wants both. Using only the recorder is how a service ships a message no client can render; using only the
server makes every test need Docker.

Neither can answer whether Gmail will accept it. That is a question about DNS and reputation and no test can
answer it.

## Things worth stealing from here

- `message.Message.Validate`: rejects header injection on every field that reaches a header, including the header
  NAMES, and rejects rather than sanitises.
- `message.Message.Recipients`: the envelope list, which is the only place Bcc exists.
- `message.lineWrapper`: wraps base64 at 76 characters, which `base64.Encoder` does not do and RFC 5322 requires.
- `message.normaliseCRLF`: collapses to LF first and then expands, because replacing `\n` with `\r\n` first turns
  an existing `\r\n` into `\r\r\n`.
- `sender.Config.RequireTLS`: defaults to true, because `smtp.SendMail`'s silent fallback to plaintext is the
  worst default in the standard library's mail support.
- `sender.classify`: the 4xx/5xx split, from the `*textproto.Error` that `net/smtp` already gives you.
- `mailpit.Clear` called at the START of a test, so a failing test does not poison the next one's assertions.
