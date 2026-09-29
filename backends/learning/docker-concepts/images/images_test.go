// Package images_test builds every Dockerfile and measures what each one costs and what each one breaks.
//
// # Why this is a test and not a README table
//
// A README table goes stale. These numbers are produced by building the images, so a base-image update or a Go
// release moves them and the test that asserts a RATIO still passes while the logged figures change.
//
// The assertions are on ratios and on behaviour, never on an absolute size, for the same reason every other module
// in this repo asserts on the machine-independent quantity.
package images_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/docker-concepts/internal/dockertest"
)

// moduleRoot is the build context: the directory holding go.mod.
//
// `go test` runs each package's tests in that package's directory, so a relative path to the Dockerfiles is wrong
// from here. Walking up to the go.mod is the reliable way and is the same trick database-concepts used to find its
// migrations.
func moduleRoot(t testing.TB) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the working directory")
		}

		dir = parent
	}
}

// built holds the images, built once for the package.
//
// Building seven images takes most of the suite's time, and several tests need more than one, so they are built in
// TestMain-like fashion by the first test that asks. A per-test build would take four times as long.
var built = map[string]string{}

func image(t testing.TB, name string) string {
	t.Helper()

	dockertest.Require(t)

	if tag, ok := built[name]; ok {
		return tag
	}

	root := moduleRoot(t)

	tag := "learn-go-docker-test:" + name

	dockertest.Build(t, root,
		filepath.Join(root, "dockerfiles", name+".Dockerfile"),
		tag,
		map[string]string{
			"VERSION":    "1.2.3",
			"COMMIT":     "abc1234",
			"BUILD_TIME": "2025-06-01T12:00:00Z",
		})

	built[name] = tag

	return tag
}

// TestImageSizes is the comparison the whole module exists for.
func TestImageSizes(t *testing.T) {
	dockertest.Require(t)

	names := []string{
		"01-naive",
		"07-cgo",
		"02-multistage",
		"03-distroless",
		"04-scratch",
		"05-scratch-broken",
		// Built too, or nothing notices when its syntax directive stops taking effect: it
		// was ignored for months, on line 11 instead of line 1.
		"06-cached",
	}

	sizes := map[string]int64{}

	t.Log("image                size      layers  user")

	for _, name := range names {
		tag := image(t, name)

		size := dockertest.ImageSize(t, tag)
		layers := dockertest.ImageLayers(t, tag)
		user := dockertest.ImageUser(t, tag)

		if user == "" {
			user = "root"
		}

		sizes[name] = size

		t.Logf("%-20s %-9s %6d  %s", name, dockertest.FormatBytes(size), layers, user)
	}

	naive := sizes["01-naive"]
	distroless := sizes["03-distroless"]
	scratch := sizes["04-scratch"]
	broken := sizes["05-scratch-broken"]
	alpine := sizes["02-multistage"]
	cgo := sizes["07-cgo"]

	t.Logf("the naive image is %.0fx the distroless one", float64(naive)/float64(distroless))
	t.Logf("CGO costs %.0fx, because the binary needs a libc and the image needs a userland",
		float64(cgo)/float64(distroless))
	t.Logf("alpine over distroless costs %s, which is apk, busybox and musl",
		dockertest.FormatBytes(alpine-distroless))
	t.Logf("scratch saves %s over distroless, which is the CA certificates and the zone database",
		dockertest.FormatBytes(distroless-scratch))
	t.Logf("and copying those two in costs %s over an empty scratch",
		dockertest.FormatBytes(scratch-broken))

	// The assertions are all RATIOS or ORDERINGS, so a base-image update moves the numbers and the
	// test still means something.
	if naive < 20*distroless {
		t.Errorf("the naive image is only %.1fx the distroless one; the comparison has changed",
			float64(naive)/float64(distroless))
	}

	if distroless > alpine {
		t.Errorf("distroless (%d) is larger than alpine (%d)", distroless, alpine)
	}

	if scratch > distroless {
		t.Errorf("scratch (%d) is larger than distroless (%d)", scratch, distroless)
	}

	if cgo < 5*distroless {
		t.Errorf("the CGO image is only %.1fx the distroless one", float64(cgo)/float64(distroless))
	}

	t.Log("the scratch-to-distroless gap is the whole argument: a megabyte or two for the " +
		"certificates and the zone database, in exchange for not having to remember to copy " +
		"them and not having to explain to the next person why HTTPS stopped working")
}

// TestTheNaiveImageRunsAsRootAndUsesShellForm.
func TestNaiveImageProblems(t *testing.T) {
	tag := image(t, "01-naive")

	user := dockertest.ImageUser(t, tag)

	t.Logf("USER: %q", user)

	if user != "" {
		t.Errorf("the naive image sets a user (%q); it is meant to demonstrate running as root", user)
	}

	entrypoint, command := dockertest.ImageEntrypoint(t, tag)

	t.Logf("ENTRYPOINT: %s", entrypoint)
	t.Logf("CMD:        %s", command)

	// Shell form becomes ["/bin/sh","-c","..."], which is how a Dockerfile ends up with a shell as
	// PID 1.
	if !strings.Contains(command, "/bin/sh") {
		t.Errorf("the CMD is not in shell form: %s", command)
	}

	t.Log("shell form makes /bin/sh PID 1 and the server its child. A shell does not forward " +
		"SIGTERM, so docker stop waits out the grace period and then kills the container, and " +
		"in-flight requests are dropped on every deploy.")
}

// TestSignalHandling is the measurement for the shell-form problem.
func TestSignalHandling(t *testing.T) {
	dockertest.Require(t)

	const grace = 10 * time.Second

	for _, tc := range []struct {
		name     string
		image    string
		forwards bool
	}{
		{"exec form (distroless)", "03-distroless", true},
		{"shell form (naive)", "01-naive", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tag := image(t, tc.image)

			c, err := dockertest.RunAndWait(t, tag, nil, 30*time.Second)
			if err != nil {
				t.Fatalf("starting %s: %v", tag, err)
			}

			elapsed, exitCode := c.Stop(t, grace)

			t.Logf("docker stop with a %v grace period: exited after %v with code %d",
				grace, elapsed.Round(100*time.Millisecond), exitCode)

			if tc.forwards {
				// A process that handles SIGTERM exits in milliseconds.
				if elapsed > 3*time.Second {
					t.Errorf("took %v; the signal was not handled", elapsed)
				}

				// Exit code 0, because the handler ran and the shutdown completed.
				if exitCode != 0 {
					t.Errorf("exit code %d, want 0 for a clean shutdown", exitCode)
				}

				t.Log("the binary is PID 1, received SIGTERM, ran its shutdown and exited 0")
			} else {
				// A shell as PID 1 ignores SIGTERM, so docker waits out the grace period and
				// sends SIGKILL. Exit code 137 is 128 + 9.
				if elapsed < grace-2*time.Second {
					t.Errorf("exited after %v, which is before the grace period; the "+
						"shell forwarded the signal after all", elapsed)
				}

				if exitCode != 137 {
					t.Logf("exit code %d rather than 137 (SIGKILL)", exitCode)
				}

				t.Logf("waited the full %v and was killed. Every in-flight request was "+
					"dropped, and the only symptom is a slow deploy.", grace)
			}
		})
	}

	t.Log("this is the difference between CMD [\"/app/server\"] and CMD /app/server, and it is " +
		"invisible until something has to stop gracefully")
}

// TestWhatScratchIsMissing is the control that measures each absence.
func TestWhatScratchIsMissing(t *testing.T) {
	dockertest.Require(t)

	working := image(t, "04-scratch")
	brokenImage := image(t, "05-scratch-broken")

	t.Run("with the certificates and the zone database", func(t *testing.T) {
		c, err := dockertest.RunAndWait(t, working, nil, 30*time.Second)
		if err != nil {
			t.Fatalf("starting: %v", err)
		}

		// TLS works.
		tls := c.GetJSON(t, "/tls?url=https://example.com")

		t.Logf("outbound HTTPS: %v", tls)

		if errMsg, bad := tls["error"]; bad {
			t.Logf("HTTPS failed, which may be the sandbox rather than the image: %v", errMsg)
		}

		// Time zones work.
		tz := c.GetJSON(t, "/tz?name=Europe/London")

		t.Logf("time zone: %v", tz)

		if _, bad := tz["error"]; bad {
			t.Errorf("LoadLocation failed with the zone database copied in: %v", tz)
		}

		if got := tz["abbreviation"]; got != "BST" {
			t.Errorf("Europe/London in July reports %v, want BST", got)
		}

		// A numeric user, because there is no /etc/passwd.
		info := c.GetJSON(t, "/buildinfo")

		t.Logf("uid=%v gid=%v cgo=%v", info["uid"], info["gid"], info["cgo"])

		if info["uid"] == float64(0) {
			t.Error("the scratch image runs as root")
		}

		if info["cgo"] != false {
			t.Errorf("cgo is %v; a scratch image needs a static binary", info["cgo"])
		}
	})

	t.Run("with nothing", func(t *testing.T) {
		c, err := dockertest.RunAndWait(t, brokenImage, nil, 30*time.Second)
		if err != nil {
			t.Fatalf("starting: %v", err)
		}

		// The server RUNS, because a static Go binary needs nothing. That is the surprise: an
		// empty scratch image is not broken, it is subtly incomplete.
		t.Log("the server started, because a static Go binary needs no files at all")

		// Time zones silently become UTC rather than failing... or fail. Which one is the finding.
		tz := c.GetJSON(t, "/tz?name=Europe/London")

		t.Logf("time zone without the database: %v", tz)

		if _, bad := tz["error"]; bad {
			t.Log("LoadLocation FAILED, which is the better of the two outcomes: it errors " +
				"rather than silently returning UTC")
		} else if got := tz["abbreviation"]; got == "UTC" {
			t.Error("LoadLocation silently returned UTC for Europe/London, which is a wrong " +
				"answer with no error")
		}

		// TLS fails with a certificate error.
		tlsResult := c.GetJSON(t, "/tls?url=https://example.com")

		t.Logf("outbound HTTPS without certificates: %v", tlsResult)

		if errMsg, bad := tlsResult["error"]; bad {
			t.Logf("failed as expected: %v", errMsg)
		} else {
			t.Logf("HTTPS succeeded, which means the Go binary found certificates somewhere: %v",
				tlsResult)
		}

		// And there is no shell, so no debugging.
		out, err := c.Exec(t, "sh", "-c", "echo hello")

		t.Logf("docker exec sh: %v (%s)", err, strings.TrimSpace(out))

		if err == nil {
			t.Error("a scratch image has a shell, which it should not")
		}
	})

	t.Log("an empty scratch image is not broken, which is what makes it dangerous: it starts, " +
		"serves traffic, and gets time zones and certificate verification wrong")
}

// TestDistrolessHasNoShell, which is the security property and the debugging cost.
func TestDistrolessHasNoShell(t *testing.T) {
	tag := image(t, "03-distroless")

	c, err := dockertest.RunAndWait(t, tag, nil, 30*time.Second)
	if err != nil {
		t.Fatalf("starting: %v", err)
	}

	for _, argv := range [][]string{
		{"sh"},
		{"/bin/sh", "-c", "id"},
		{"ls", "/"},
		{"cat", "/etc/passwd"},
	} {
		out, err := c.Exec(t, argv...)

		t.Logf("exec %-24s %v", strings.Join(argv, " "), firstLine(out))

		if err == nil {
			t.Errorf("%v succeeded in a distroless image", argv)
		}
	}

	t.Log("no shell, no ls, no cat. An attacker with RCE has nothing to run and no way to fetch a " +
		"payload, and neither do you: debugging is `docker cp` and a sidecar with the same " +
		"filesystem, or the :debug tag which puts busybox back and gives up the property.")

	// It DOES have what the program needs.
	info := c.GetJSON(t, "/buildinfo")

	t.Logf("version=%v commit=%v uid=%v", info["version"], info["commit"], info["uid"])

	// The ldflags landed, which is the other thing this image tests.
	if info["version"] != "1.2.3" {
		t.Errorf("version is %v, want 1.2.3; the -X flags did not take", info["version"])
	}
	if info["commit"] != "abc1234" {
		t.Errorf("commit is %v", info["commit"])
	}

	// nonroot, from the image tag rather than a USER line.
	if info["uid"] == float64(0) {
		t.Error("the distroless:nonroot image is running as root")
	}

	t.Logf("the :nonroot tag set the user to %v without a USER line in the Dockerfile", info["uid"])
}

// TestLdflagsInjectionIsSilentWhenWrong is the trap in -X.
func TestLdflagsInjectionIsSilentWhenWrong(t *testing.T) {
	dockertest.Require(t)

	root := moduleRoot(t)

	// The same Dockerfile with a WRONG variable path. -X on a path that does not exist is not an
	// error: the linker has nothing to patch and says nothing.
	broken := filepath.Join(t.TempDir(), "broken.Dockerfile")

	content, err := os.ReadFile(filepath.Join(root, "dockerfiles", "03-distroless.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}

	// main.version becomes main.Version, which does not exist (the variable is lower-case).
	wrong := strings.ReplaceAll(string(content), "-X main.version=", "-X main.Version=")

	if wrong == string(content) {
		t.Fatal("the substitution did not apply")
	}

	if err := os.WriteFile(broken, []byte(wrong), 0o600); err != nil {
		t.Fatal(err)
	}

	tag := "learn-go-docker-test:wrong-ldflags"

	dockertest.Build(t, root, broken, tag, map[string]string{
		"VERSION": "1.2.3", "COMMIT": "abc1234", "BUILD_TIME": "2025-06-01T12:00:00Z",
	})

	c, err := dockertest.RunAndWait(t, tag, nil, 30*time.Second)
	if err != nil {
		t.Fatalf("starting: %v", err)
	}

	info := c.GetJSON(t, "/buildinfo")

	t.Logf("with -X main.Version (capital V): version=%v commit=%v",
		info["version"], info["commit"])

	// The build SUCCEEDED and the variable kept its default.
	if info["version"] != "unknown" {
		t.Errorf("version is %v; expected the default because the -X path was wrong",
			info["version"])
	}

	// The other flag, whose path was right, DID take. So one wrong path does not break the others,
	// which makes it even harder to notice.
	if info["commit"] != "abc1234" {
		t.Errorf("commit is %v; the correct -X should still have applied", info["commit"])
	}

	t.Log("-X on a variable that does not exist is not an error. The build succeeds, the variable " +
		"keeps its default, and the other flags still work, so a typo produces one wrong field " +
		"in a health endpoint and nothing else.")

	t.Log("which is why main.go defaults them to \"unknown\" rather than \"\": a binary built by " +
		"the wrong pipeline says so instead of looking like a field nobody filled in")
}

// TestLayerCachingIsAboutOrdering.
func TestLayerCachingIsAboutOrdering(t *testing.T) {
	dockertest.Require(t)

	naive := image(t, "01-naive")
	staged := image(t, "02-multistage")

	t.Logf("the naive image has %d layers and the staged one %d",
		dockertest.ImageLayers(t, naive), dockertest.ImageLayers(t, staged))

	// The measurable difference is in the BUILD, not the image, and it is about what a source change
	// invalidates. A build with `COPY . .` before `go mod download` reruns the download on every
	// commit; one with go.mod copied first does not.
	//
	// Measuring a rebuild time here would be measuring the Docker cache, which is shared with every
	// other test in this package and is therefore not a controlled experiment. So this asserts on the
	// STRUCTURE, which is the thing a reader can check in their own Dockerfile.
	root := moduleRoot(t)

	// The INSTRUCTIONS, not the file text.
	//
	// The first version of this searched the raw file for "go mod download" and found it in a COMMENT
	// explaining the caching, 150 bytes before the RUN line that does it, so the ordering assertion
	// failed on a Dockerfile that was correct. A file whose comments discuss its own instructions
	// cannot be checked by substring position.
	naiveSteps := instructions(t, filepath.Join(root, "dockerfiles", "01-naive.Dockerfile"))
	stagedSteps := instructions(t, filepath.Join(root, "dockerfiles", "02-multistage.Dockerfile"))

	t.Log("naive:")
	for i, line := range naiveSteps {
		t.Logf("  %d  %s", i, line)
	}

	t.Log("staged:")
	for i, line := range stagedSteps {
		t.Logf("  %d  %s", i, line)
	}

	naiveCopyAll := indexOf(naiveSteps, "COPY . .")
	naiveDownload := indexOf(naiveSteps, "go mod download")

	stagedCopyMod := indexOf(stagedSteps, "COPY go.mod")
	stagedCopyAll := indexOf(stagedSteps, "COPY . .")
	stagedDownload := indexOf(stagedSteps, "go mod download")

	// The naive one has no separate download step at all: `go build` downloads as a side effect, so
	// every source change re-downloads every dependency.
	if naiveDownload >= 0 {
		t.Errorf("the naive Dockerfile has a go mod download step at instruction %d", naiveDownload)
	}

	if naiveCopyAll < 0 {
		t.Fatal("the naive Dockerfile has no COPY . .")
	}

	if stagedCopyMod < 0 || stagedDownload < 0 || stagedCopyAll < 0 {
		t.Fatalf("the staged Dockerfile is missing a step: copy-mod=%d download=%d copy-all=%d",
			stagedCopyMod, stagedDownload, stagedCopyAll)
	}

	// go.mod, then download, then the source. That order is the whole optimisation.
	if stagedCopyMod >= stagedDownload || stagedDownload >= stagedCopyAll {
		t.Errorf("the staged Dockerfile's order is copy-mod=%d download=%d copy-all=%d; it has "+
			"to be that ascending order", stagedCopyMod, stagedDownload, stagedCopyAll)
	}

	t.Logf("staged order: COPY go.mod (%d) -> go mod download (%d) -> COPY . . (%d)",
		stagedCopyMod, stagedDownload, stagedCopyAll)

	t.Log("Docker invalidates a layer when its inputs change. `COPY . .` changes on every commit, " +
		"so anything after it reruns on every build. go.mod changes rarely, so copying it " +
		"separately means `go mod download` reruns only when a dependency changes. Two extra " +
		"lines, and it is the biggest build-time win in a Go Dockerfile.")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)

	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}

	return s
}

// instructions returns a Dockerfile's instruction lines, with comments and blanks removed.
//
// Line continuations are joined, because a RUN spanning five backslash-terminated lines is one instruction and
// searching for a word in the middle of it should find it.
func instructions(t testing.TB, path string) []string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var (
		out     []string
		current strings.Builder
	)

	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasSuffix(line, "\\") {
			current.WriteString(strings.TrimSuffix(line, "\\"))
			current.WriteString(" ")

			continue
		}

		current.WriteString(line)

		out = append(out, strings.Join(strings.Fields(current.String()), " "))

		current.Reset()
	}

	if current.Len() > 0 {
		out = append(out, strings.Join(strings.Fields(current.String()), " "))
	}

	return out
}

// indexOf returns the index of the first instruction containing sub, or -1.
func indexOf(lines []string, sub string) int {
	for i, line := range lines {
		if strings.Contains(line, sub) {
			return i
		}
	}

	return -1
}
