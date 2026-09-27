package templates_test

import (
	"strings"
	"testing"
	"time"

	htmltemplate "html/template"
	texttemplate "text/template"

	"github.com/alexvervloet/learn-go/backends/learning/email-concepts/templates"
)

func data() templates.WelcomeData {
	return templates.WelcomeData{
		Name:       "Ada Lovelace",
		Plan:       "Pro",
		ConfirmURL: "https://example.test/confirm?t=abc&u=1",
		Trial:      true,
		TrialEnds:  time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),
		Footer:     "You signed up on 1 June.",
	}
}

func TestBothBodiesRender(t *testing.T) {
	r, err := templates.New()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("templates: %v", r.Names())

	text, html, err := r.Welcome(data())
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("text:\n%s", indent(text))
	t.Logf("html:\n%s", indent(html))

	for _, want := range []string{"Ada Lovelace", "Pro", "1 July 2025"} {
		if !strings.Contains(text, want) {
			t.Errorf("the text body is missing %q", want)
		}
		if !strings.Contains(html, want) {
			t.Errorf("the HTML body is missing %q", want)
		}
	}

	// The two bodies say the same things, which is the reason they are rendered by one call: a
	// service that renders them separately ends up with an HTML body offering a discount and a
	// plain-text body that does not, and nobody reads the plain-text one so nobody notices.
	if strings.Contains(text, "<p>") {
		t.Error("the text body contains HTML")
	}
}

// TestHTMLTemplateEscapesAndTextDoesNot is the mistake the two packages make possible.
func TestHTMLTemplateEscapesAndTextDoesNot(t *testing.T) {
	// A name from a signup form.
	hostile := templates.WelcomeData{
		Name:       `Ada <script>alert("xss")</script>`,
		Plan:       "Pro",
		ConfirmURL: `https://example.test/confirm?t=abc" onmouseover="alert(1)`,
		Footer:     "Footer",
	}

	r, err := templates.New()
	if err != nil {
		t.Fatal(err)
	}

	text, html, err := r.Welcome(hostile)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("html:\n%s", indent(html))

	// html/template escaped it CONTEXTUALLY: the name in a text node, the URL in an attribute.
	if strings.Contains(html, "<script>alert") {
		t.Error("the HTML body contains an unescaped script tag")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("the script tag was not escaped:\n%s", html)
	}

	// The URL was escaped for an ATTRIBUTE, so the injected onmouseover cannot break out.
	if strings.Contains(html, `onmouseover="alert(1)"`) {
		t.Error("the URL injection broke out of the attribute")
	}

	t.Log("html/template escaped the name for a text node and the URL for an attribute, which is " +
		"CONTEXTUAL escaping: the same value in a <script> would have been escaped for " +
		"JavaScript instead")

	// The text body did NOT escape, which is correct: it is not HTML.
	if !strings.Contains(text, "<script>") {
		t.Error("the text body escaped its input, which turns an apostrophe into &#39; in an " +
			"email nobody renders as HTML")
	}

	t.Logf("the text body kept it verbatim: %q", firstLine(text))

	// And the demonstration of the mistake: the same template rendered with the WRONG package.
	const tmpl = `<p>Hello {{ .Name }}</p>`

	textRendered := renderWithText(t, tmpl, hostile)
	htmlRendered := renderWithHTML(t, tmpl, hostile)

	t.Logf("text/template on an HTML template: %s", textRendered)
	t.Logf("html/template on the same:         %s", htmlRendered)

	if !strings.Contains(textRendered, "<script>alert") {
		t.Error("text/template escaped something, which it does not do")
	}
	if strings.Contains(htmlRendered, "<script>alert") {
		t.Error("html/template did not escape")
	}

	t.Log("the two packages have the SAME API, so rendering HTML with text/template compiles, " +
		"runs, and is a cross-site scripting hole. The compiler cannot help and the only " +
		"defence is the import line.")
}

func renderWithText(t *testing.T, tmpl string, data any) string {
	t.Helper()

	parsed, err := texttemplate.New("x").Parse(tmpl)
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder

	if err := parsed.Execute(&b, data); err != nil {
		t.Fatal(err)
	}

	return b.String()
}

func renderWithHTML(t *testing.T, tmpl string, data any) string {
	t.Helper()

	parsed, err := htmltemplate.New("x").Parse(tmpl)
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder

	if err := parsed.Execute(&b, data); err != nil {
		t.Fatal(err)
	}

	return b.String()
}

// TestConditionalsDoNotLeaveGaps.
func TestConditionalsDoNotLeaveGaps(t *testing.T) {
	r, err := templates.New()
	if err != nil {
		t.Fatal(err)
	}

	withTrial, _, err := r.Welcome(data())
	if err != nil {
		t.Fatal(err)
	}

	noTrialData := data()
	noTrialData.Trial = false

	withoutTrial, _, err := r.Welcome(noTrialData)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("with a trial:\n%s", indent(withTrial))
	t.Logf("without one:\n%s", indent(withoutTrial))

	if strings.Contains(withoutTrial, "trial ends") {
		t.Error("the trial line appears when Trial is false")
	}

	// No run of blank lines in either, which is what the tidying is for: a text/template with
	// {{ if }} on its own line leaves an empty line whether or not the condition holds.
	for name, body := range map[string]string{"with": withTrial, "without": withoutTrial} {
		if strings.Contains(body, "\n\n\n") {
			t.Errorf("the %s-trial body has a run of blank lines:\n%q", name, body)
		}
	}

	// And it ends with exactly one newline.
	for name, body := range map[string]string{"with": withTrial, "without": withoutTrial} {
		if !strings.HasSuffix(body, "\n") || strings.HasSuffix(body, "\n\n") {
			t.Errorf("the %s-trial body does not end with exactly one newline: %q",
				name, body[max(len(body)-10, 0):])
		}
	}

	t.Log("the template could control this with {{- if -}} and remembering the hyphens in every " +
		"template does not survive contact with a second author, so the renderer tidies instead")
}

// TestAMissingFieldIsAnError, which is the argument for a struct over a map.
func TestAMissingFieldIsAnError(t *testing.T) {
	// A template referencing a field the struct does not have.
	const tmpl = `Hello {{ .Nmae }}`

	parsed, err := texttemplate.New("x").Parse(tmpl)
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder

	err = parsed.Execute(&b, templates.WelcomeData{Name: "Ada"})

	t.Logf("a typo'd field against a struct: %v", err)

	if err == nil {
		t.Error("a missing struct field rendered without an error")
	}

	// The same template against a MAP renders nothing and reports nothing.
	b.Reset()

	err = parsed.Execute(&b, map[string]any{"Name": "Ada"})

	t.Logf("the same template against a map: %q, error %v", b.String(), err)

	if err != nil {
		t.Logf("(this Go version reports it: %v)", err)
	} else if !strings.Contains(b.String(), "<no value>") {
		t.Errorf("a map rendered %q, which is neither an error nor a marker", b.String())
	}

	t.Log("a struct makes a typo'd field an error at Execute. A map renders <no value> or " +
		"nothing, which reaches production as an email with a blank where the name should be.")
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}
