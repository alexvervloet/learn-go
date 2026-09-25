package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Type assertions and type switches
// =================================
//
// An assertion goes from an interface back to something concrete:
//
//	v.(T)        panics if v does not hold a T
//	v, ok := v.(T)  never panics; ok reports the outcome
//
// Use the comma-ok form everywhere except where a failure genuinely means the
// program is broken. A type switch is the multi-way version.
//
// The honest caveat: a type switch over YOUR OWN types is usually a method
// that has not been written yet. Type switches earn their place at the edges,
// where data arrives untyped: JSON, reflection, plugin boundaries, AST walks.

// describeJSON is the legitimate use. encoding/json decodes into `any`, and
// every JSON value arrives as one of six Go types. There is no method to add
// to float64, so a type switch is the only tool.
//
//	JSON            Go
//	------------    -------------------
//	object          map[string]any
//	array           []any
//	string          string
//	number          float64   (always, even for integers)
//	true / false    bool
//	null            nil
func describeJSON(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return fmt.Sprintf("bool(%t)", x)
	case float64:
		// Every JSON number decodes to float64. An id of 12345678901234567890
		// loses precision here, which is why APIs send large ids as strings.
		return fmt.Sprintf("number(%g)", x)
	case string:
		return fmt.Sprintf("string(%q)", x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, describeJSON(item))
		}
		return "array[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		// Map order is randomised, so sort for stable output. Same lesson as 02.
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s: %s", k, describeJSON(x[k])))
		}
		return "object{" + strings.Join(parts, ", ") + "}"
	default:
		// Reachable if someone passes a value that did not come from json.
		return fmt.Sprintf("unhandled %T", x)
	}
}

// parseJSON decodes into any so describeJSON has something real to walk.
func parseJSON(raw string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("parse %q: %w", raw, err)
	}
	return v, nil
}

// assertToAnInterface is the underused form: you can assert to an INTERFACE,
// not only a concrete type, to ask "does this value happen to also do X?".
//
// The standard library does this constantly. io.Copy asks whether its source
// implements WriterTo and its destination implements ReaderFrom, and takes a
// fast path if either does.
func assertToAnInterface(v any) (greeting string, isGreeter bool) {
	if g, ok := v.(Greeter); ok {
		return g.Greet(), true
	}
	return "", false
}

// comparingAssertionForms shows the panic the comma-ok form avoids.
func comparingAssertionForms(v any) (safeResult string, safeOK bool, panicMessage string) {
	safeResult, safeOK = func() (string, bool) {
		s, ok := v.(string)
		return s, ok
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				panicMessage = fmt.Sprint(r)
			}
		}()
		_ = v.(string) // no comma-ok: panics when v is not a string
	}()

	return safeResult, safeOK, panicMessage
}

// Embedding
// =========
//
// Embedding promotes the embedded type's methods onto the outer type, which is
// how Go composes behaviour without inheritance. The outer type satisfies every
// interface the embedded one did, for free.

// baseLogger is the embedded behaviour.
type baseLogger struct{ prefix string }

// Log is promoted to anything embedding baseLogger.
func (b baseLogger) Log(msg string) string { return b.prefix + ": " + msg }

// Service embeds baseLogger, so Service has a Log method it never declared.
type Service struct {
	baseLogger // embedded: no field name
	Name       string
}

// Logger is satisfied by Service through promotion alone.
type Logger interface{ Log(msg string) string }

var _ Logger = Service{} // Service never declares Log

// overriding shows that a method on the outer type shadows the promoted one,
// and that the embedded version is still reachable by field name. This is
// Go's nearest equivalent to calling super().
type LoudService struct {
	baseLogger
	Name string
}

// Log shadows baseLogger.Log and delegates to it explicitly.
func (s LoudService) Log(msg string) string {
	return strings.ToUpper(s.baseLogger.Log(msg))
}

// demoAssertions prints type switches, interface assertions, and embedding.
func demoAssertions() {
	raw := `{"id": 7, "name": "Ana", "tags": ["go", "backend"], "active": true, "manager": null}`
	v, err := parseJSON(raw)
	if err != nil {
		fmt.Printf("  parse failed: %v\n", err)
		return
	}
	fmt.Printf("  describeJSON:\n    %s\n", describeJSON(v))

	greeting, ok := assertToAnInterface(Loud("hey"))
	fmt.Printf("  assert Loud to Greeter: %q, ok=%t\n", greeting, ok)
	_, ok = assertToAnInterface(42)
	fmt.Printf("  assert int  to Greeter: ok=%t\n", ok)

	s, sok, panicMsg := comparingAssertionForms(42)
	fmt.Printf("  comma-ok on an int: %q, ok=%t   bare assertion: panic: %s\n", s, sok, panicMsg)

	svc := Service{baseLogger: baseLogger{prefix: "svc"}, Name: "users"}
	fmt.Printf("  embedded method promoted: %s\n", svc.Log("started"))

	loud := LoudService{baseLogger: baseLogger{prefix: "svc"}, Name: "users"}
	fmt.Printf("  outer method shadows it:  %s\n", loud.Log("started"))
	fmt.Printf("  embedded still reachable: %s\n", loud.baseLogger.Log("started"))
}
