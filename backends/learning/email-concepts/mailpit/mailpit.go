// Package mailpit talks to a local mail catcher, so a test can read what was actually sent.
//
// # Why a mail catcher rather than a mock
//
// A recorder (sender.Recorder) proves the caller built the right message. It cannot prove the message is VALID,
// because nothing parses it.
//
// Mailpit is an SMTP server that accepts everything and exposes it over an HTTP API. So a test sends a real
// message through a real SMTP conversation, then reads back the parsed result: the decoded subject, the
// separated parts, the attachments with their filenames. That is the only way to catch a header that is
// double-encoded or an attachment whose base64 has no line breaks.
//
// # What it does not catch
//
// Deliverability. Mailpit accepts a message with no SPF, no DKIM, no DMARC alignment and a From address on a
// domain you do not own, and so does every local server. Whether Gmail accepts it is a question about DNS and
// reputation that no test can answer.
package mailpit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// DefaultSMTPPort and DefaultAPIURL are the compose file's.
const (
	DefaultSMTPHost = "localhost"
	DefaultSMTPPort = 1026
	DefaultAPIURL   = "http://localhost:8026"
)

// SMTPHost returns the host from the environment or the default.
func SMTPHost() string {
	if h := os.Getenv("MAILPIT_HOST"); h != "" {
		return h
	}
	return DefaultSMTPHost
}

// SMTPPort returns the port.
func SMTPPort() int {
	if p := os.Getenv("MAILPIT_SMTP_PORT"); p != "" {
		var n int
		if _, err := fmt.Sscanf(p, "%d", &n); err == nil {
			return n
		}
	}
	return DefaultSMTPPort
}

// APIURL returns the HTTP API base.
func APIURL() string {
	if u := os.Getenv("MAILPIT_API_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return DefaultAPIURL
}

var (
	once     sync.Once
	ok       bool
	checkErr error
)

// Require skips the test unless Mailpit is reachable.
func Require(t testing.TB) {
	t.Helper()

	once.Do(func() { ok, checkErr = check() })

	if !ok {
		t.Skipf("no Mailpit at %s (%v)\n"+
			"  docker compose up -d\n"+
			"  or point it elsewhere: MAILPIT_API_URL=http://host:8025 "+
			"MAILPIT_SMTP_PORT=1025 go test ./...",
			APIURL(), checkErr)
	}
}

func check() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", APIURL()+"/api/v1/info", nil)
	if err != nil {
		return false, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("the info endpoint returned %d", resp.StatusCode)
	}

	return true, nil
}

// Clear deletes every captured message.
//
// Called at the START of a test rather than the end. A test that cleans up afterwards leaves the mailbox full
// when it fails, and the next test's assertions are about the failed test's messages. Cleaning up first makes
// each test's state depend only on itself.
func Clear(t testing.TB) {
	t.Helper()

	Require(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "DELETE", APIURL()+"/api/v1/messages", nil)
	if err != nil {
		t.Fatalf("building the delete request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("clearing the mailbox: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clearing the mailbox returned %d", resp.StatusCode)
	}
}

// Summary is one message in the list.
type Summary struct {
	ID      string    `json:"ID"`
	From    Contact   `json:"From"`
	To      []Contact `json:"To"`
	Cc      []Contact `json:"Cc"`
	Bcc     []Contact `json:"Bcc"`
	Subject string    `json:"Subject"`
	Size    int       `json:"Size"`
}

// Contact is an address as Mailpit parsed it.
type Contact struct {
	Name    string `json:"Name"`
	Address string `json:"Address"`
}

// Message is a full message as Mailpit parsed it.
type Message struct {
	ID      string    `json:"ID"`
	From    Contact   `json:"From"`
	To      []Contact `json:"To"`
	Cc      []Contact `json:"Cc"`
	Bcc     []Contact `json:"Bcc"`
	ReplyTo []Contact `json:"ReplyTo"`
	Subject string    `json:"Subject"`

	// Text and HTML are the DECODED bodies, so a quoted-printable or base64 part arrives as the
	// original string. That is what makes an assertion on the body meaningful.
	Text string `json:"Text"`
	HTML string `json:"HTML"`

	Attachments []Attachment `json:"Attachments"`
	Inline      []Attachment `json:"Inline"`

	// Date as Mailpit parsed it from the header.
	Date time.Time `json:"Date"`
}

// Attachment is a part Mailpit treated as a file.
type Attachment struct {
	PartID      string `json:"PartID"`
	FileName    string `json:"FileName"`
	ContentType string `json:"ContentType"`
	ContentID   string `json:"ContentID"`
	Size        int    `json:"Size"`
}

// List returns the captured messages, newest first.
func List(t testing.TB) []Summary {
	t.Helper()

	var body struct {
		Total    int       `json:"total"`
		Messages []Summary `json:"messages"`
	}

	get(t, "/api/v1/messages", &body)

	return body.Messages
}

// Get returns one message in full.
func Get(t testing.TB, id string) Message {
	t.Helper()

	var m Message

	get(t, "/api/v1/message/"+id, &m)

	return m
}

// Raw returns the message as MAILPIT STORED IT, which is not the same as what was on the wire.
//
// Mailpit PREPENDS headers it derived or added: Bcc reconstructed from the envelope, Message-ID, Return-Path and
// Received. So this is useful for checking the MIME structure (boundaries, part order, encodings) and useless for
// asserting that a header is absent, because the absent header may be one Mailpit adds.
//
// That cost a confusing test failure: an assertion that the raw message contains no Bcc header failed, and the
// builder was right. The only place to answer "what did we send" is the builder's own output.
func Raw(t testing.TB, id string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET",
		APIURL()+"/api/v1/message/"+id+"/raw", nil)
	if err != nil {
		t.Fatalf("building the raw request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("fetching the raw message: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the raw message: %v", err)
	}

	return string(raw)
}

// AttachmentContent downloads one attachment's bytes.
func AttachmentContent(t testing.TB, id, partID string) []byte {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET",
		APIURL()+"/api/v1/message/"+id+"/part/"+partID, nil)
	if err != nil {
		t.Fatalf("building the part request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("fetching the part: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the part: %v", err)
	}

	return content
}

// WaitForCount polls until the mailbox holds n messages.
//
// Polling, because SMTP delivery and the HTTP API are not synchronised: Send returns when the server accepted the
// DATA phase, and Mailpit stores it a moment later. A test that lists immediately sees nothing, and a sleep long
// enough to be safe is either flaky or slow.
func WaitForCount(t testing.TB, n int, timeout time.Duration) []Summary {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		messages := List(t)

		if len(messages) >= n {
			return messages
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out after %v waiting for %d message(s); the mailbox holds %d",
		timeout, n, len(List(t)))

	return nil
}

func get(t testing.TB, path string, into any) {
	t.Helper()

	Require(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", APIURL()+path, nil)
	if err != nil {
		t.Fatalf("building the request for %s: %v", path, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("fetching %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s returned %d: %s", path, resp.StatusCode, body)
	}

	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
}
