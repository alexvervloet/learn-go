// Package cliflags builds a command-line interface with the standard library.
//
// # Why flag and not cobra
//
// cobra is what most Go CLIs use and it is 20,000 lines to get subcommands, completion and generated docs. The
// standard `flag` package gets you a flag set, typed parsing, a usage message and subcommands in about fifty
// lines, and knowing where the line is means knowing when the dependency is worth it.
//
// What you give up: no shell completion, no nested subcommands without writing the recursion, no automatic
// man pages, and a usage message you will want to replace. That is a fair summary of when to reach for cobra.
//
// # What this package shows that a tutorial usually does not
//
//   - Subcommands, which `flag` supports through a FlagSet per command and nothing else.
//   - A custom flag.Value, which is how a flag parses into a type the package does not know about, and the
//     only way to get a repeatable flag.
//   - Environment fallback done in the right ORDER: a flag beats an environment variable beats a default.
//   - Writing usage to a buffer instead of os.Stderr, which is what makes any of it testable.
package cliflags

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is what a parsed command line produces.
type Config struct {
	Command string

	Verbose bool
	Format  string
	Timeout time.Duration
	Workers int

	// Tags is a repeatable flag: -tag a -tag b. The flag package has no built-in for this, so it is a
	// flag.Value whose Set appends rather than replaces.
	Tags []string

	// Args are what is left after the flags.
	Args []string
}

// stringList is a flag.Value that accumulates.
//
// # The two methods and what each is for
//
// String() is called to render the DEFAULT in the usage message, and it is called on a zero value that the flag
// package constructs by reflection, so it must not panic on a nil receiver.
//
// Set() is called once per occurrence. Appending rather than assigning is the whole difference between a
// repeatable flag and one that takes the last value.
type stringList []string

// String renders the default in the usage message. It is called on a zero value the flag package builds by
// reflection, so it must not panic on a nil receiver.
func (s *stringList) String() string {
	if s == nil {
		return ""
	}

	return strings.Join(*s, ",")
}

// Set is called once per occurrence of the flag.
func (s *stringList) Set(value string) error {
	if value == "" {
		return errors.New("empty value")
	}

	*s = append(*s, value)

	return nil
}

// ErrUsage is returned when -h was asked for, so a caller can exit 0 rather than 2.
//
// `flag` returns ErrHelp for this and every tutorial ignores it, which is why so many Go programs exit 2 when
// you ask them for help. An exit code of 2 from `mytool --help` breaks a script that checks it.
var ErrUsage = errors.New("cliflags: usage requested")

// ErrNoCommand is returned when no subcommand was given.
var ErrNoCommand = errors.New("cliflags: no command")

// Commands are the subcommands this tool has.
var Commands = []string{"serve", "migrate", "version"}

// Parse builds a Config from arguments and an environment lookup.
//
// # Why the environment is a parameter
//
// Because os.Getenv makes this untestable in parallel: the process environment is global, so two tests setting
// the same variable race in a way the detector cannot see. Passing the lookup means a test supplies a map.
//
// # Why output is a parameter
//
// Because flag writes usage to os.Stderr by default, and a test that wants to assert on the usage message
// cannot capture that without replacing a global.
func Parse(args []string, output io.Writer, getenv func(string) string) (*Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	if len(args) == 0 {
		// The write error is dropped on purpose: there is nothing to do about a failure to print usage, and
		// reporting it instead of the real error would bury the reason the parse failed.
		_, _ = fmt.Fprintln(output, usage())

		return nil, ErrNoCommand
	}

	command := args[0]

	if command == "-h" || command == "--help" || command == "help" {
		_, _ = fmt.Fprintln(output, usage())

		return nil, ErrUsage
	}

	if !isCommand(command) {
		_, _ = fmt.Fprintf(output, "unknown command %q\n\n%s\n", command, usage())

		return nil, fmt.Errorf("cliflags: unknown command %q", command)
	}

	cfg := &Config{Command: command}

	// A FlagSet per subcommand, which is how `flag` does subcommands and the only way it does.
	//
	// ContinueOnError, not ExitOnError. The default panics the process on a bad flag, which is fine for a
	// main and impossible to test, and turns a library into something that can kill its caller.
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(output)

	var tags stringList

	fs.BoolVar(&cfg.Verbose, "v", false, "log every step")
	fs.StringVar(&cfg.Format, "format", envOr(getenv, "TOOL_FORMAT", "text"), "output format: text or json")
	fs.DurationVar(&cfg.Timeout, "timeout", envDuration(getenv, "TOOL_TIMEOUT", 30*time.Second), "how long to wait")
	fs.IntVar(&cfg.Workers, "workers", envInt(getenv, "TOOL_WORKERS", 4), "how many workers")
	fs.Var(&tags, "tag", "a tag; repeat for several")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(output, "usage: tool %s [flags] [args]\n\nflags:\n", command)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args[1:]); err != nil {
		// flag.ErrHelp means -h was given, which is a SUCCESS for the user and an error for the API. Turning
		// it into a distinguishable error is what lets main exit 0.
		if errors.Is(err, flag.ErrHelp) {
			return nil, ErrUsage
		}

		return nil, fmt.Errorf("cliflags: %w", err)
	}

	cfg.Tags = tags
	cfg.Args = fs.Args()

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validate checks what the flag package cannot.
//
// `flag` gives type safety and nothing else: -format=xml parses fine and is not a format this tool has. Every
// constraint beyond the type lives here, and putting it in one function rather than scattering it through the
// command means one place to read.
func validate(cfg *Config) error {
	switch cfg.Format {
	case "text", "json":
	default:
		return fmt.Errorf("cliflags: -format must be text or json, got %q", cfg.Format)
	}

	if cfg.Workers < 1 {
		return fmt.Errorf("cliflags: -workers must be at least 1, got %d", cfg.Workers)
	}

	if cfg.Timeout <= 0 {
		return fmt.Errorf("cliflags: -timeout must be positive, got %v", cfg.Timeout)
	}

	return nil
}

func isCommand(name string) bool {
	for _, c := range Commands {
		if c == name {
			return true
		}
	}

	return false
}

// usage is the top-level help.
func usage() string {
	var b strings.Builder

	b.WriteString("usage: tool <command> [flags] [args]\n\ncommands:\n")

	for _, c := range Commands {
		fmt.Fprintf(&b, "  %s\n", c)
	}

	b.WriteString("\nrun `tool <command> -h` for a command's flags")

	return b.String()
}

// envOr reads a string from the environment.
//
// # The precedence, and why it is this way round
//
// A flag beats an environment variable beats a default. That is achieved by using the environment value as the
// flag's DEFAULT, so an explicitly passed flag overwrites it and an absent one leaves it.
//
// The naive alternative, reading the environment after parsing and overwriting, gets the precedence backwards:
// the environment would beat the flag, and a user could not override a variable set by their shell profile.
func envOr(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}

	return fallback
}

// envInt reads an int, falling back on anything unparseable.
//
// Falling back rather than failing, because an environment variable is often set by something other than the
// user running the command, and killing the process over a typo in a CI variable is worse than using the
// default. A FLAG with a bad value does fail, because the user typed it.
func envInt(getenv func(string) string, key string, fallback int) int {
	if n, err := strconv.Atoi(getenv(key)); err == nil {
		return n
	}

	return fallback
}

func envDuration(getenv func(string) string, key string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(getenv(key)); err == nil {
		return d
	}

	return fallback
}
