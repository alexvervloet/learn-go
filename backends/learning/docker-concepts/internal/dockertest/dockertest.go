// Package dockertest builds and runs images from a test.
//
// # Why shell out to docker rather than use the SDK
//
// github.com/docker/docker is a large dependency that pulls in most of Docker's internals, and the SDK's API for
// building an image is "produce a tar of the context yourself". Shelling out to the CLI is what a developer
// actually does, so a test that shells out is testing the same thing the reader will run.
//
// The cost is that the test depends on a `docker` binary and on its output format. Both are stable enough, and
// the alternative is 200 lines of tar-building.
package dockertest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	once      sync.Once
	available bool
	checkErr  error
)

// Require skips the test unless Docker is usable.
func Require(t testing.TB) {
	t.Helper()

	once.Do(func() { available, checkErr = check() })

	if !available {
		t.Skipf("Docker is not available (%v)\n"+
			"  these tests build and run images, so they need a working daemon",
			checkErr)
	}
}

func check() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// `docker info`, not `docker version`. Version answers from the client alone, so it succeeds when
	// the daemon is not running, which is exactly the case this has to catch.
	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")

	out, err := cmd.Output()
	if err != nil {
		return false, err
	}

	if strings.TrimSpace(string(out)) == "" {
		return false, errors.New("the daemon reported no version")
	}

	return true, nil
}

// Build builds an image and returns its tag.
//
// The context directory is the module root, which is where the Dockerfiles expect to be run from. Passing it
// explicitly rather than assuming the working directory, because `go test ./...` runs each package's tests in that
// package's directory.
func Build(t testing.TB, contextDir, dockerfile, tag string, args map[string]string) string {
	t.Helper()

	Require(t)

	// Ten minutes, because a cold build pulls golang:1.27 and gcr.io/distroless, which on a slow
	// connection is most of that.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	argv := []string{"build", "-f", dockerfile, "-t", tag}

	for k, v := range args {
		argv = append(argv, "--build-arg", k+"="+v)
	}

	argv = append(argv, contextDir)

	cmd := exec.CommandContext(ctx, "docker", argv...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("building %s from %s: %v\n%s", tag, dockerfile, err, stderr.String())
	}

	return tag
}

// ImageSize returns an image's size in bytes.
//
// `docker image inspect --format {{.Size}}`, which is the UNCOMPRESSED size of the layers as the daemon stores
// them. That is not the size of a `docker push`: the registry stores compressed layers, so a pull is smaller than
// this number, usually by half for anything with text in it.
//
// Both numbers are legitimate and they answer different questions. This one is disk on the node; the compressed
// one is bytes over the network on a cold start.
func ImageSize(t testing.TB, tag string) int64 {
	t.Helper()

	out := run(t, "image", "inspect", tag, "--format", "{{.Size}}")

	size, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		t.Fatalf("parsing the size of %s (%q): %v", tag, out, err)
	}

	return size
}

// ImageLayers returns how many layers an image has.
//
// Worth knowing because a registry stores and transfers layers, and the per-layer overhead is real: an image with
// forty layers of one file each pulls more slowly than one with four, even at the same total size.
func ImageLayers(t testing.TB, tag string) int {
	t.Helper()

	out := run(t, "image", "inspect", tag, "--format", "{{len .RootFS.Layers}}")

	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("parsing the layer count of %s (%q): %v", tag, out, err)
	}

	return n
}

// ImageUser returns the USER the image runs as, or "" for root.
func ImageUser(t testing.TB, tag string) string {
	t.Helper()

	return strings.TrimSpace(run(t, "image", "inspect", tag, "--format", "{{.Config.User}}"))
}

// ImageEntrypoint returns the entrypoint and command, for checking exec against shell form.
func ImageEntrypoint(t testing.TB, tag string) (entrypoint, command string) {
	t.Helper()

	entrypoint = strings.TrimSpace(run(t, "image", "inspect", tag, "--format", "{{json .Config.Entrypoint}}"))
	command = strings.TrimSpace(run(t, "image", "inspect", tag, "--format", "{{json .Config.Cmd}}"))

	return entrypoint, command
}

// Container is a running container.
type Container struct {
	ID   string
	Port int
	tag  string
}

// Run starts a container with its port published on a free host port.
//
// Port 0 published, so the OS picks. A fixed host port in a test suite is a conflict waiting for two packages to
// run at once, which `go test ./...` does by default.
func Run(t testing.TB, tag string, env map[string]string) *Container {
	t.Helper()

	Require(t)

	port, err := freePort()
	if err != nil {
		t.Fatalf("finding a free port: %v", err)
	}

	argv := []string{"run", "-d", "-p", fmt.Sprintf("127.0.0.1:%d:8080", port)}

	for k, v := range env {
		argv = append(argv, "-e", k+"="+v)
	}

	argv = append(argv, tag)

	out := run(t, argv...)

	id := strings.TrimSpace(out)

	c := &Container{ID: id, Port: port, tag: tag}

	t.Cleanup(func() {
		// The logs BEFORE removing it, because a container that failed to start has its
		// explanation there and nowhere else. Fetching them only on failure would mean the
		// cleanup has to know whether the test failed, which t.Cleanup cannot see.
		if logs := c.Logs(t); logs != "" && testing.Verbose() {
			t.Logf("logs from %s:\n%s", tag, indent(logs))
		}

		// -f because a healthy container is still running, and --volumes because a container
		// with an anonymous volume leaves it behind and `docker system df` grows forever.
		_ = exec.Command("docker", "rm", "-f", "--volumes", id).Run()
	})

	return c
}

// RunAndWait starts a container and waits for its health endpoint.
//
// Returns an error rather than failing, because several tests here EXPECT a container not to start and asserting
// on that is the point.
func RunAndWait(t testing.TB, tag string, env map[string]string, timeout time.Duration) (*Container, error) {
	t.Helper()

	c := Run(t, tag, env)

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if !c.Running(t) {
			return c, fmt.Errorf("the container exited: %s", firstLines(c.Logs(t), 5))
		}

		if c.Healthy() {
			return c, nil
		}

		time.Sleep(50 * time.Millisecond)
	}

	return c, fmt.Errorf("timed out after %v waiting for %s to become healthy: %s",
		timeout, tag, firstLines(c.Logs(t), 5))
}

// Running reports whether the container is still up.
func (c *Container) Running(t testing.TB) bool {
	t.Helper()

	out, err := exec.Command("docker", "inspect", c.ID, "--format", "{{.State.Running}}").Output()
	if err != nil {
		return false
	}

	return strings.TrimSpace(string(out)) == "true"
}

// ExitCode returns the container's exit code, or -1 if it is still running.
func (c *Container) ExitCode(t testing.TB) int {
	t.Helper()

	if c.Running(t) {
		return -1
	}

	out := run(t, "inspect", c.ID, "--format", "{{.State.ExitCode}}")

	code, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return -1
	}

	return code
}

// Logs returns the container's combined output.
func (c *Container) Logs(t testing.TB) string {
	t.Helper()

	// CombinedOutput, because a Go program's slog output goes to stdout and a panic goes to stderr,
	// and a test looking at one of them misses the other.
	out, err := exec.Command("docker", "logs", c.ID).CombinedOutput()
	if err != nil {
		return string(out)
	}

	return string(out)
}

// Healthy reports whether the service answers.
func (c *Container) Healthy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", c.URL()+"/healthz", nil)
	if err != nil {
		return false
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}

	_ = resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// URL returns the container's base URL on the host.
func (c *Container) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", c.Port)
}

// GetJSON fetches a JSON endpoint.
func (c *Container) GetJSON(t testing.TB, path string) map[string]any {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", c.URL()+path, nil)
	if err != nil {
		t.Fatalf("building the request for %s: %v", path, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("fetching %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var out map[string]any

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}

	return out
}

// Stop sends SIGTERM and reports how long the container took to exit.
//
// This is the signal-handling test. `docker stop` sends SIGTERM, waits, then sends SIGKILL. A container whose PID
// 1 forwards the signal exits in milliseconds; one whose PID 1 is a shell waits out the full grace period and is
// killed.
func (c *Container) Stop(t testing.TB, grace time.Duration) (time.Duration, int) {
	t.Helper()

	seconds := int(grace.Seconds())
	if seconds < 1 {
		seconds = 1
	}

	start := time.Now()

	cmd := exec.Command("docker", "stop", "-t", strconv.Itoa(seconds), c.ID)

	if err := cmd.Run(); err != nil {
		t.Fatalf("stopping %s: %v", c.ID, err)
	}

	elapsed := time.Since(start)

	return elapsed, c.ExitCode(t)
}

// Exec runs a command in the container, which fails on an image with no shell.
func (c *Container) Exec(t testing.TB, argv ...string) (string, error) {
	t.Helper()

	full := append([]string{"exec", c.ID}, argv...)

	out, err := exec.Command("docker", full...).CombinedOutput()

	return string(out), err
}

func run(t testing.TB, argv ...string) string {
	t.Helper()

	cmd := exec.Command("docker", argv...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(argv, " "), err, stderr.String())
	}

	return string(out)
}

// freePort asks the OS for a port and closes the listener.
//
// A small race: another process could take the port between the close and the container's bind. The alternative is
// letting Docker pick with -p 8080 and then reading the mapping back, which is a second docker call and no race.
// For a test suite the race has never been observed and the note is here so a reader knows it exists.
func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()

	return listener.Addr().(*net.TCPAddr).Port, nil
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")

	if len(lines) > n {
		lines = lines[:n]
	}

	return strings.Join(lines, "; ")
}

// FormatBytes renders a size the way docker images does, so a log line reads like the CLI's output.
func FormatBytes(b int64) string {
	const unit = 1000

	if b < unit {
		return fmt.Sprintf("%dB", b)
	}

	// Decimal units, because that is what `docker images` reports: 8.8MB is 8,800,000 bytes and not
	// 8 MiB. Mixing the two is how a comparison against the CLI's output looks wrong by 5%.
	div, exp := int64(unit), 0

	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "kMGTPE"[exp])
}
