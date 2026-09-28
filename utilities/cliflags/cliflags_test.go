package cliflags

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// env builds a lookup from a map, so no test touches the process environment.
func env(kv map[string]string) func(string) string {
	return func(key string) string { return kv[key] }
}

// TestDefaultsApplyWithNoFlags is the baseline.
func TestDefaultsApplyWithNoFlags(t *testing.T) {
	var out bytes.Buffer

	cfg, err := Parse([]string{"serve"}, &out, env(nil))
	require.NoError(t, err)

	require.Equal(t, "serve", cfg.Command)
	require.False(t, cfg.Verbose)
	require.Equal(t, "text", cfg.Format)
	require.Equal(t, 30*time.Second, cfg.Timeout)
	require.Equal(t, 4, cfg.Workers)
	require.Empty(t, cfg.Tags)
	require.Empty(t, out.String(), "a successful parse says nothing")
}

// TestAFlagBeatsTheEnvironmentBeatsTheDefault is the precedence, in one test.
//
// The naive implementation reads the environment AFTER parsing and overwrites, which makes the environment beat
// the flag and means a user cannot override a variable their shell profile set.
func TestAFlagBeatsTheEnvironmentBeatsTheDefault(t *testing.T) {
	var out bytes.Buffer

	// Nothing set: the default.
	cfg, err := Parse([]string{"serve"}, &out, env(nil))
	require.NoError(t, err)
	require.Equal(t, "text", cfg.Format)
	require.Equal(t, 4, cfg.Workers)

	// The environment: it wins over the default.
	cfg, err = Parse([]string{"serve"}, &out, env(map[string]string{
		"TOOL_FORMAT": "json", "TOOL_WORKERS": "16", "TOOL_TIMEOUT": "5s",
	}))
	require.NoError(t, err)
	require.Equal(t, "json", cfg.Format)
	require.Equal(t, 16, cfg.Workers)
	require.Equal(t, 5*time.Second, cfg.Timeout)

	// The flag: it wins over the environment.
	cfg, err = Parse([]string{"serve", "-format", "text", "-workers", "2"}, &out, env(map[string]string{
		"TOOL_FORMAT": "json", "TOOL_WORKERS": "16",
	}))
	require.NoError(t, err)
	require.Equal(t, "text", cfg.Format)
	require.Equal(t, 2, cfg.Workers)
}

// TestARepeatableFlagAccumulates is what a custom flag.Value is for.
func TestARepeatableFlagAccumulates(t *testing.T) {
	var out bytes.Buffer

	cfg, err := Parse([]string{"serve", "-tag", "a", "-tag", "b", "-tag", "c"}, &out, env(nil))
	require.NoError(t, err)

	require.Equal(t, []string{"a", "b", "c"}, cfg.Tags,
		"Set appends; the built-in StringVar would have kept only the last")
}

// TestAnEmptyRepeatedValueIsRejected covers the Set error path.
func TestAnEmptyRepeatedValueIsRejected(t *testing.T) {
	var out bytes.Buffer

	_, err := Parse([]string{"serve", "-tag", ""}, &out, env(nil))
	require.Error(t, err)
	require.Contains(t, out.String(), "empty value", "the flag package prints what Set returned")
}

// TestAnUnparseableEnvironmentValueFallsBack rather than killing the process.
//
// An environment variable is often set by something other than the person running the command, and dying over
// a typo in a CI variable is worse than using the default. A FLAG with a bad value does fail, because the user
// typed it.
func TestAnUnparseableEnvironmentValueFallsBack(t *testing.T) {
	var out bytes.Buffer

	cfg, err := Parse([]string{"serve"}, &out, env(map[string]string{
		"TOOL_WORKERS": "lots", "TOOL_TIMEOUT": "a while",
	}))
	require.NoError(t, err)
	require.Equal(t, 4, cfg.Workers)
	require.Equal(t, 30*time.Second, cfg.Timeout)

	// The same value as a flag is an error.
	_, err = Parse([]string{"serve", "-workers", "lots"}, &out, env(nil))
	require.Error(t, err)
}

// TestValidationCatchesWhatTypesCannot is the layer above flag.
func TestValidationCatchesWhatTypesCannot(t *testing.T) {
	var out bytes.Buffer

	for _, args := range [][]string{
		{"serve", "-format", "xml"},
		{"serve", "-workers", "0"},
		{"serve", "-workers", "-3"},
		{"serve", "-timeout", "0s"},
		{"serve", "-timeout", "-5s"},
	} {
		_, err := Parse(args, &out, env(nil))
		require.Error(t, err, "%v parses as the right TYPE and is still wrong", args)
	}
}

// TestHelpIsNotAFailure is the exit-code bug in half the Go CLIs in existence.
func TestHelpIsNotAFailure(t *testing.T) {
	for _, args := range [][]string{
		{"-h"},
		{"--help"},
		{"help"},
		{"serve", "-h"},
	} {
		var out bytes.Buffer

		_, err := Parse(args, &out, env(nil))
		require.ErrorIs(t, err, ErrUsage, "%v", args)
		require.NotEmpty(t, out.String(), "%v printed nothing", args)
	}
}

// TestUsageListsTheCommands covers the top-level help.
func TestUsageListsTheCommands(t *testing.T) {
	var out bytes.Buffer

	_, err := Parse(nil, &out, env(nil))
	require.ErrorIs(t, err, ErrNoCommand)

	for _, command := range Commands {
		require.Contains(t, out.String(), command)
	}
}

// TestAnUnknownCommandSaysSoAndPrintsUsage is the shape a user needs.
func TestAnUnknownCommandSaysSoAndPrintsUsage(t *testing.T) {
	var out bytes.Buffer

	_, err := Parse([]string{"srve"}, &out, env(nil))
	require.ErrorContains(t, err, `unknown command "srve"`)
	require.Contains(t, out.String(), "serve", "the usage follows, so the typo is obvious")
}

// TestSubcommandFlagsAreScoped is what a FlagSet per command means.
func TestSubcommandFlagsAreScoped(t *testing.T) {
	var out bytes.Buffer

	cfg, err := Parse([]string{"migrate", "-v", "up", "3"}, &out, env(nil))
	require.NoError(t, err)

	require.Equal(t, "migrate", cfg.Command)
	require.True(t, cfg.Verbose)
	require.Equal(t, []string{"up", "3"}, cfg.Args,
		"positional arguments after the flags belong to the subcommand")
}

// TestFlagsAfterPositionalArgumentsAreNotParsed is a real trap in the flag package.
//
// `flag` stops at the first NON-FLAG argument. Everything after it is positional, including things that look
// like flags. `tool serve up -v` leaves Verbose false and puts "-v" in Args, which is standard library
// behaviour, differs from GNU getopt, and surprises everyone once.
func TestFlagsAfterPositionalArgumentsAreNotParsed(t *testing.T) {
	var out bytes.Buffer

	cfg, err := Parse([]string{"serve", "up", "-v"}, &out, env(nil))
	require.NoError(t, err)

	require.False(t, cfg.Verbose, "flag stops parsing at the first non-flag argument")
	require.Equal(t, []string{"up", "-v"}, cfg.Args, "and the rest is positional, dash and all")

	// The same flags before the argument work, which is the shape to document in the usage message.
	cfg, err = Parse([]string{"serve", "-v", "up"}, &out, env(nil))
	require.NoError(t, err)
	require.True(t, cfg.Verbose)
}

// TestUsageIsWrittenToTheProvidedWriter is what makes any of this testable.
func TestUsageIsWrittenToTheProvidedWriter(t *testing.T) {
	var out bytes.Buffer

	_, err := Parse([]string{"serve", "-h"}, &out, env(nil))
	require.ErrorIs(t, err, ErrUsage)

	text := out.String()
	require.Contains(t, text, "-format")
	require.Contains(t, text, "-workers")
	require.Contains(t, text, "-tag")
	require.True(t, strings.HasPrefix(text, "usage: tool serve"),
		"flag writes to os.Stderr by default, which no test can capture without replacing a global")
}
