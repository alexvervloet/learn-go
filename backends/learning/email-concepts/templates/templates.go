// Package templates renders email bodies, and the reason there are two template packages.
//
// # html/template against text/template
//
// The HTML body uses html/template and the plain-text body uses text/template, and they are NOT
// interchangeable. html/template escapes its output contextually: a value in an attribute is escaped for an
// attribute, one inside a <script> for JavaScript, one in a URL for a URL. text/template escapes nothing.
//
// So rendering HTML with text/template is a cross-site scripting hole, and rendering plain text with
// html/template turns an apostrophe into &#39; in an email nobody will render as HTML.
//
// Getting that backwards is the single most common templating mistake in Go, and both packages have the same API
// so the compiler cannot help.
//
// # Why embed
//
// The templates are embedded with go:embed, so the binary has no runtime dependency on a directory. An email
// service that reads its templates from disk fails in a container that did not copy them, at the moment it tries
// to send, which is the worst time to find out.
//
// The cost is that changing a template needs a deploy. For transactional email that is right: the template is
// code and it is reviewed and versioned with the code that fills it.
package templates

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
	"time"
)

// files holds the templates.
//
// The pattern is explicit rather than `all:files` because embedding a directory silently includes whatever else
// is in it, and a stray editor backup in the binary is a real thing.
//
//go:embed files/*.tmpl
var files embed.FS

// WelcomeData is what the welcome templates need.
//
// A named struct rather than a map, and the reason is that a template referencing a field the struct does not
// have is a RUNTIME error with a map (or worse, renders as nothing) and a compile-time-ish error with a struct:
// text/template reports `can't evaluate field Nmae` at Execute, which is at least a clear failure at the moment
// the template is parsed against the data.
type WelcomeData struct {
	Name       string
	Plan       string
	ConfirmURL string
	Trial      bool
	TrialEnds  time.Time
	Footer     string
}

// Renderer holds the parsed templates.
//
// Parsed ONCE at construction, not per send. template.Parse is not cheap, it is not safe to reparse
// concurrently into the same template, and a service that parses per email spends more time parsing than sending.
type Renderer struct {
	html *htmltemplate.Template
	text *texttemplate.Template
}

// New parses the templates.
//
// # Why the errors are returned rather than panicked
//
// A template that will not parse is a programming error, so `template.Must` and a panic at init is the
// conventional answer and it is right for a package with no configuration. This returns an error because the
// FuncMap below is a place where a caller could add a function, and a panic at init in a library makes the
// binary unstartable with a stack trace instead of a message.
func New() (*Renderer, error) {
	funcs := texttemplate.FuncMap{
		// A currency formatter, because the alternative is a float in a template and money as a
		// float is the mistake database-concepts measured.
		"money": func(cents int64) string {
			return fmt.Sprintf("£%d.%02d", cents/100, cents%100)
		},
	}

	text, err := texttemplate.New("text").Funcs(funcs).
		ParseFS(files, "files/*.txt.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parsing the text templates: %w", err)
	}

	// The FuncMap is shared between the two packages without a conversion, because
	// text/template.FuncMap and html/template.FuncMap are the same type: `map[string]any`. So one map
	// serves both, and a function that returns HTML-unsafe output is equally unsafe in both, which is
	// worth knowing before putting a "raw HTML" helper in one.
	html, err := htmltemplate.New("html").
		Funcs(funcs).
		ParseFS(files, "files/*.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parsing the HTML templates: %w", err)
	}

	return &Renderer{html: html, text: text}, nil
}

// Welcome renders both bodies.
//
// Returning both from one call, because they have to agree: a service that renders them separately ends up with
// an HTML body offering a discount and a plain-text body that does not, and nobody reads the plain-text one so
// nobody notices.
func (r *Renderer) Welcome(data WelcomeData) (text, html string, err error) {
	var textBuf, htmlBuf bytes.Buffer

	// The template NAME is the file's base name, which ParseFS sets. So "welcome.txt.tmpl", not
	// "welcome": getting it wrong gives "no template named welcome" at run time and there is no way
	// to check it at compile time.
	if err := r.text.ExecuteTemplate(&textBuf, "welcome.txt.tmpl", data); err != nil {
		return "", "", fmt.Errorf("rendering the text body: %w", err)
	}

	if err := r.html.ExecuteTemplate(&htmlBuf, "welcome.html.tmpl", data); err != nil {
		return "", "", fmt.Errorf("rendering the HTML body: %w", err)
	}

	// The text body is tidied because a template with {{ if }} blocks leaves blank lines where the
	// conditions were false, and an email full of them looks broken. The HTML body is not, because
	// whitespace there is invisible.
	return tidyText(textBuf.String()), htmlBuf.String(), nil
}

// tidyText collapses runs of blank lines and trims the ends.
//
// A text/template with conditionals produces a body with gaps: `{{ if .Trial }}` on its own line leaves an empty
// line whether or not the condition holds. The template can control that with `{{- if -}}`, and remembering the
// hyphens in every template is the version that does not survive contact with a second author.
func tidyText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")

	var out []string

	blank := 0

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blank++

			// One blank line is a paragraph break. Two or more is a gap where a condition was.
			if blank > 1 {
				continue
			}

			out = append(out, "")

			continue
		}

		blank = 0

		out = append(out, strings.TrimRight(line, " \t"))
	}

	return strings.Trim(strings.Join(out, "\n"), "\n") + "\n"
}

// Names lists the parsed templates, for a startup log line.
//
// Worth having: "which templates does this binary contain" is the first question when an email renders empty,
// and the answer is otherwise only in the embed directive.
func (r *Renderer) Names() []string {
	var out []string

	for _, t := range r.text.Templates() {
		if t.Name() != "text" {
			out = append(out, t.Name())
		}
	}

	for _, t := range r.html.Templates() {
		if t.Name() != "html" {
			out = append(out, t.Name())
		}
	}

	return out
}
