// Package apitest starts the service against a real Postgres for a test.
//
// # Why a real database
//
// Because the interesting parts of this service are in the database: the unique constraints that make the
// slug and email checks race-free, the transaction that derives a slug from an id, the tuple comparison that
// makes keyset pagination stable, the cascade that cleans up a deleted user's URLs. A fake store tests none of
// them and passes.
//
// The tests skip when there is no database. A skip is not a pass, which is why CI runs them with one and fails
// the build if it sees a skip message.
//
// # Isolation
//
// A database per test BINARY and a truncate per test. The binary name comes from os.Args[0], which ends in
// <pkg>.test, because `go test ./...` runs packages concurrently and two packages truncating one database is
// a deadlock in one of them and a wrong query plan in the other. That lesson is recorded in the repository's
// LESSONS.md and this is the same fix.
package apitest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/api"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/auth"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/cache"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/store"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	// The blank import registers "pgx" as a database/sql driver.
	//
	// goose talks to database/sql, and pgxpool does not. Without this line goose fails with
	// `sql: unknown driver "pgx" (forgotten import?)`, and the parenthesis in that message is the standard
	// library telling you exactly this.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/pressly/goose/v3"
	"github.com/redis/go-redis/v9"
)

// DefaultURL is a local Postgres reached over the socket, which is what a brew or apt install gives you.
//
// No host, no user, no password: libpq's rules then use the Unix socket, the OS username, and peer
// authentication. That is the repository's convention, and it means a developer with a local Postgres and a
// learn_go_db needs no environment variable at all.
const DefaultURL = "postgres:///learn_go_db?sslmode=disable"

// ComposeURL is what docker-compose.yml in this directory serves.
//
// Port 5433, so the container does not collide with a local Postgres on 5432. The collision is worth avoiding
// precisely because it is invisible: Docker binds *:5432 and a local Postgres binds 127.0.0.1:5432, `localhost`
// resolves to the second, and every test skips with "role postgres does not exist" while a healthy container
// sits there.
//
//	DATABASE_URL="postgres://postgres:postgres@localhost:5433/learn_go_db?sslmode=disable" go test ./...
const ComposeURL = "postgres://postgres:postgres@localhost:5433/learn_go_db?sslmode=disable"

// URL returns the connection string.
func URL() string {
	if u := os.Getenv("DATABASE_URL"); u != "" {
		return u
	}

	return DefaultURL
}

// DefaultRedisAddr is a local Redis on its usual port.
const DefaultRedisAddr = "localhost:6379"

// ComposeRedisAddr is what docker-compose.yml serves, on 6382 to avoid the same collision.
const ComposeRedisAddr = "localhost:6382"

// RedisAddr returns the Redis address.
func RedisAddr() string {
	if a := os.Getenv("REDIS_ADDR"); a != "" {
		return a
	}

	return DefaultRedisAddr
}

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool

	// Two errors, not one.
	//
	// connectErr means there is no database, which is a SKIP: a developer without Docker running should get a
	// message, not a failure. setupErr means the database is there and something else went wrong, which is a
	// FAILURE, because a skip would hide it.
	//
	// The first version had one error and skipped on both. goose could not find its driver, every test
	// skipped with "no database available", and the real message `unknown driver "pgx"` was buried in the
	// skip text where nothing looks for it.
	connectErr error
	setupErr   error

	// effectiveURL is the connection string naming THIS binary's database.
	//
	// It exists because pgxpool.Config.ConnString() returns the string the config was PARSED FROM. connect()
	// sets cfg.ConnConfig.Database after parsing, so ConnString still names the base database, and goose
	// cheerfully migrated that one while the tests truncated the per-binary one:
	// `relation "clicks" does not exist`.
	//
	// This repository has the same bug recorded in LESSONS.md from the database module, and it was written
	// again here within the hour. A getter named after an input returns the input.
	effectiveURL string
)

// Pool returns the shared pool, or skips the test.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()

	poolOnce.Do(openPoolOnce)

	if connectErr != nil {
		t.Skipf("no database available at %s (%v).\n"+
			"  docker compose up -d, then DATABASE_URL=%q go test ./...\n"+
			"  or point it anywhere: DATABASE_URL=postgres:///mydb go test ./...",
			URL(), connectErr, ComposeURL)
	}

	if setupErr != nil {
		t.Fatalf("the database at %s is reachable and could not be prepared: %v", URL(), setupErr)
	}

	return pool
}

// openPoolOnce fills pool, connectErr and setupErr.
func openPoolOnce() {
	p, err := connect()
	if err != nil {
		connectErr = err

		return
	}

	if err := migrate(p); err != nil {
		p.Close()

		setupErr = err

		return
	}

	pool = p
}

// connect creates this binary's own database and returns a pool on it.
func connect() (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(URL())
	if err != nil {
		return nil, err
	}

	name, err := ensureOwnDatabase(ctx, cfg)
	if err != nil {
		return nil, err
	}

	cfg.ConnConfig.Database = name
	cfg.MaxConns = 10

	effectiveURL = rewriteDatabase(URL(), name)

	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	if err := p.Ping(ctx); err != nil {
		p.Close()

		return nil, err
	}

	return p, nil
}

// ensureOwnDatabase creates learn_go_shortener_<binary> if it is not there.
func ensureOwnDatabase(ctx context.Context, cfg *pgxpool.Config) (string, error) {
	base := filepath.Base(os.Args[0])
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".test")

	var b strings.Builder

	for _, r := range strings.ToLower(base) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}

	name := "learn_go_shortener_" + b.String()

	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		return "", err
	}

	defer admin.Close()

	var exists bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return "", err
	}

	if !exists {
		// CREATE DATABASE cannot be parameterised and cannot run inside a transaction. The name is built from
		// os.Args[0] and filtered to [a-z0-9_] above, so there is nothing to inject, and the quoting is there
		// because an unquoted identifier is folded to lowercase and a quoted one is not.
		if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, name)); err != nil {
			// A concurrent binary may have created it between the check and here. 42P04 is duplicate_database
			// and it is a success for this purpose.
			if !strings.Contains(err.Error(), "42P04") && !strings.Contains(err.Error(), "already exists") {
				return "", err
			}
		}
	}

	return name, nil
}

// rewriteDatabase replaces the database name in a connection string, in either of the two forms pgx accepts.
func rewriteDatabase(original, database string) string {
	if parsed, err := url.Parse(original); err == nil && parsed.Scheme != "" {
		parsed.Path = "/" + database

		return parsed.String()
	}

	fields := strings.Fields(original)
	replaced := false

	for i, f := range fields {
		if strings.HasPrefix(f, "dbname=") {
			fields[i] = "dbname=" + database
			replaced = true
		}
	}

	if !replaced {
		fields = append(fields, "dbname="+database)
	}

	return strings.Join(fields, " ")
}

// migrate applies the goose migrations.
func migrate(_ *pgxpool.Pool) error {
	db, err := goose.OpenDBWithDriver("pgx", effectiveURL)
	if err != nil {
		return err
	}

	defer func() { _ = db.Close() }()

	goose.SetBaseFS(nil)
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	dir, err := migrationsDir()
	if err != nil {
		return err
	}

	return goose.Up(db, dir)
}

// migrationsDir walks up to the module root and finds migrations/.
func migrationsDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		candidate := filepath.Join(dir, "migrations")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("apitest: no migrations directory above %s", dir)
		}

		dir = parent
	}
}

// Truncate empties every table.
//
// # Why TRUNCATE ... RESTART IDENTITY CASCADE
//
// TRUNCATE is much faster than DELETE on a table with rows, because it does not scan. RESTART IDENTITY resets
// the sequences, so every test's first URL has id 1 and a test can assert on a slug. CASCADE is required
// because urls references users, and truncating users alone is an error.
func Truncate(t testing.TB, p *pgxpool.Pool) {
	t.Helper()

	if _, err := p.Exec(context.Background(),
		`TRUNCATE clicks, urls, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// Store returns a store on a truncated database.
func Store(t testing.TB) *store.Store {
	t.Helper()

	p := Pool(t)
	Truncate(t, p)

	return store.New(p)
}

// RedisClient returns a Redis client on this binary's own database, or skips.
func RedisClient(t testing.TB) *redis.Client {
	t.Helper()

	client := redis.NewClient(&redis.Options{Addr: RedisAddr(), DB: redisDB()})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()

		t.Skipf("no Redis at %s (%v).\n"+
			"  docker compose up -d, then REDIS_ADDR=%s go test ./...",
			RedisAddr(), err, ComposeRedisAddr)
	}

	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush redis: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// redisDB picks a database from the binary name, leaving 0 for a human.
func redisDB() int {
	base := filepath.Base(os.Args[0])

	var sum int
	for _, r := range base {
		sum = (sum*31 + int(r)) % 15
	}

	return sum + 1
}

// FakeEnqueuer records tasks instead of sending them.
//
// # Why a fake here and a real Redis elsewhere
//
// The API's contract is that a redirect ENQUEUES a click. Whether asynq then delivers it is asynq's contract,
// tested in the repository's jobs-concepts module against a real Redis. Separating them means the API tests do
// not need a worker running, and a failure points at one thing.
type FakeEnqueuer struct {
	mu    sync.Mutex
	tasks []*asynq.Task

	// Err, when set, makes every enqueue fail. The redirect must still succeed, which is the assertion that
	// matters.
	Err error
}

// EnqueueContext records the task.
func (f *FakeEnqueuer) EnqueueContext(_ context.Context, task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.Err != nil {
		return nil, f.Err
	}

	f.tasks = append(f.tasks, task)

	return &asynq.TaskInfo{ID: fmt.Sprintf("fake-%d", len(f.tasks))}, nil
}

// Tasks returns what was enqueued.
func (f *FakeEnqueuer) Tasks() []*asynq.Task {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]*asynq.Task(nil), f.tasks...)
}

// Reset clears the record.
func (f *FakeEnqueuer) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.tasks = nil
}

// Harness is a running service plus the things a test pokes at it with.
type Harness struct {
	Server   *httptest.Server
	Store    *store.Store
	Cache    *cache.Cache
	Enqueuer *FakeEnqueuer
	Secret   []byte
	BaseURL  string
}

// HarnessOptions configures the harness.
type HarnessOptions struct {
	// WithCache attaches a real Redis-backed cache. Off by default, so a test that is not about caching does
	// not need Redis and does not have to reason about a value being served from it.
	WithCache bool

	CacheTTL    time.Duration
	NegativeTTL time.Duration
	TokenTTL    time.Duration
}

// New starts the service.
func New(t *testing.T, opts HarnessOptions) *Harness {
	t.Helper()

	st := Store(t)

	h := &Harness{
		Store:    st,
		Enqueuer: &FakeEnqueuer{},
		Secret:   []byte("a-test-secret-that-is-at-least-32-bytes-long"),
		BaseURL:  "https://short.test",
	}

	if opts.WithCache {
		h.Cache = cache.New(RedisClient(t), cache.Options{
			TTL:         opts.CacheTTL,
			NegativeTTL: opts.NegativeTTL,
		})
	}

	if opts.TokenTTL == 0 {
		opts.TokenTTL = time.Hour
	}

	srv := api.New(api.Options{
		Store:    st,
		Cache:    h.Cache,
		Enqueuer: h.Enqueuer,
		// Discard, not os.Stderr. A passing test prints nothing, and a handler logging to stderr from a
		// goroutine after the test ends is a data race the detector finds.
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Secret:   h.Secret,
		TokenTTL: opts.TokenTTL,
		BaseURL:  h.BaseURL,
		// The minimum cost. At the production cost of 12, a test suite with thirty registrations spends eight
		// seconds hashing, and the pressure to fix that lands on the production constant.
		HashCost: auth.TestCost,
	})

	h.Server = httptest.NewServer(srv.Routes())
	t.Cleanup(h.Server.Close)

	return h
}

// Client returns an HTTP client that does NOT follow redirects.
//
// # Why that matters here
//
// The default client follows a 302, so a test of the redirect handler would see the response from
// example.com rather than the 302 itself, and would make a real network request to do it. Returning
// ErrUseLastResponse stops at the redirect, which is the thing under test.
func (h *Harness) Client() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Response is a finished exchange.
//
// # Why this and not *http.Response
//
// Because *http.Response hands a test an open body and the obligation to close it, and a test that forgets
// leaks a connection. Twenty-nine call sites is twenty-nine chances to forget, and the linter found several.
//
// Reading the body and closing it inside Do moves the obligation to one place. The test gets bytes, which is
// what it wanted, and there is nothing left to leak.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body into v.
func (r *Response) JSON(t *testing.T, v any) {
	t.Helper()

	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %d response %q: %v", r.Status, r.Body, err)
	}
}

// Do sends a request to the harness and reads the whole response.
func (h *Harness) Do(t *testing.T, method, path, token string, body any) *Response {
	t.Helper()

	return h.DoWith(t, method, path, nil, body, func(req *http.Request) {
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	})
}

// DoWith is Do with the request in hand, for a test that needs a particular header.
//
// The variadic customiser rather than a headers map, because two of these tests care about the EXACT spelling
// and case of a header, and a map would go through textproto's canonicalisation on the way in.
func (h *Harness) DoWith(t *testing.T, method, path string, _ map[string]string, body any, customise ...func(*http.Request)) *Response {
	t.Helper()

	var reader io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}

		reader = strings.NewReader(string(encoded))
	}

	req, err := http.NewRequestWithContext(context.Background(), method, h.Server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for _, c := range customise {
		c(req)
	}

	resp, err := h.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}

	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: raw}
}

// Register creates a user and returns the token.
func (h *Harness) Register(t *testing.T, email, password string) string {
	t.Helper()

	resp := h.Do(t, http.MethodPost, "/api/v1/register", "", map[string]string{
		"email":    email,
		"password": password,
	})

	if resp.Status != http.StatusCreated {
		t.Fatalf("register %s: %d %s", email, resp.Status, resp.Body)
	}

	var body struct {
		Token string `json:"token"`
	}

	resp.JSON(t, &body)

	return body.Token
}
