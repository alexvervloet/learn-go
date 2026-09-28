// Package apitest starts the service against a real Postgres and Redis for a test.
//
// # Why this looks like the url-shortener's harness
//
// Because the isolation problem is the same and the solution is the same: a database per test BINARY, derived
// from os.Args[0], and a truncate per test. `go test ./...` runs package binaries concurrently, and two of them
// truncating one database is a deadlock in one and a wrong query plan in the other.
//
// It is a copy rather than a shared package on purpose. The two capstones are separate modules, so a reader can
// clone one directory and run it, and a shared testing module between them would be a dependency that exists
// only for this.
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

	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/api"
	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/auth"
	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/ratelimit"
	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"

	// Registers "pgx" as a database/sql driver, which goose needs and pgxpool does not provide.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/pressly/goose/v3"
	"github.com/redis/go-redis/v9"
)

// DefaultURL is a local Postgres over the socket, which is what a brew or apt install gives you.
const DefaultURL = "postgres:///learn_go_db?sslmode=disable"

// ComposeURL is what docker-compose.yml in this directory serves.
//
// Port 5434, so the container does not collide with a local Postgres on 5432 or with the url-shortener's
// container on 5433. Docker binds *:5432 and a local Postgres binds 127.0.0.1:5432, `localhost` resolves to the
// second, and every test then skips with a confusing authentication error while a healthy container sits there.
const ComposeURL = "postgres://postgres:postgres@localhost:5434/learn_go_db?sslmode=disable"

// URL returns the connection string.
func URL() string {
	if u := os.Getenv("DATABASE_URL"); u != "" {
		return u
	}

	return DefaultURL
}

// DefaultRedisAddr is a local Redis on its usual port.
const DefaultRedisAddr = "localhost:6379"

// ComposeRedisAddr is what docker-compose.yml serves.
const ComposeRedisAddr = "localhost:6383"

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

	// Two errors, not one. connectErr is a SKIP (there is no database); setupErr is a FAILURE (there is one
	// and something else went wrong). Collapsing them hides the second inside a skip message nobody reads.
	connectErr error
	setupErr   error

	// effectiveURL names THIS binary's database.
	//
	// pgxpool.Config.ConnString() returns the string the config was PARSED FROM, so it still names the base
	// database after ConnConfig.Database is changed. goose migrating the base database while the tests
	// truncate the per-binary one is `relation "bookmarks" does not exist`, and it is written down in this
	// repository's LESSONS.md twice.
	effectiveURL string
)

// Pool returns the shared pool, or skips.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()

	poolOnce.Do(openPoolOnce)

	if connectErr != nil {
		t.Skipf("no database available at %s (%v).\n"+
			"  docker compose up -d, then DATABASE_URL=%q go test ./...",
			URL(), connectErr, ComposeURL)
	}

	if setupErr != nil {
		t.Fatalf("the database at %s is reachable and could not be prepared: %v", URL(), setupErr)
	}

	return pool
}

func openPoolOnce() {
	p, err := connect()
	if err != nil {
		connectErr = err

		return
	}

	if err := migrate(); err != nil {
		p.Close()

		setupErr = err

		return
	}

	pool = p
}

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

// ensureOwnDatabase creates learn_go_bookmarks_<binary> if it is not there.
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

	name := "learn_go_bookmarks_" + b.String()

	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		return "", err
	}

	defer admin.Close()

	var exists bool
	if err := admin.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return "", err
	}

	if !exists {
		// CREATE DATABASE cannot be parameterised and cannot run in a transaction. The name is filtered to
		// [a-z0-9_] above, so there is nothing to inject; the quoting is there because an unquoted identifier
		// is folded to lowercase and a quoted one is not.
		if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, name)); err != nil {
			// 42P04 is duplicate_database: another binary won the race, which is a success here.
			if !strings.Contains(err.Error(), "42P04") && !strings.Contains(err.Error(), "already exists") {
				return "", err
			}
		}
	}

	return name, nil
}

// rewriteDatabase replaces the database name in either connection-string form.
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
func migrate() error {
	db, err := goose.OpenDBWithDriver("pgx", effectiveURL)
	if err != nil {
		return err
	}

	defer func() { _ = db.Close() }()

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

// migrationsDir walks up to the module root.
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
// RESTART IDENTITY resets the sequences, so ids are predictable. CASCADE is required because the tables
// reference each other and truncating users alone is an error.
func Truncate(t testing.TB, p *pgxpool.Pool) {
	t.Helper()

	if _, err := p.Exec(context.Background(),
		`TRUNCATE bookmark_tags, bookmarks, tags, categories, refresh_tokens, users RESTART IDENTITY CASCADE`,
	); err != nil {
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

// RedisClient returns a flushed Redis client on this binary's own database, or skips.
func RedisClient(t testing.TB) *redis.Client {
	t.Helper()

	client := redis.NewClient(&redis.Options{Addr: RedisAddr(), DB: redisDB()})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()

		t.Skipf("no Redis at %s (%v).\n  docker compose up -d, then REDIS_ADDR=%s go test ./...",
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

// Harness is a running service.
type Harness struct {
	Server *httptest.Server
	Store  *store.Store
	Redis  *redis.Client
	Secret []byte
}

// HarnessOptions configures it.
type HarnessOptions struct {
	AccessTTL  time.Duration
	RefreshTTL time.Duration

	// LoginLimit and LoginWindow configure the rate limiter on the credential endpoints. Zero means the
	// default, which is generous enough that a test not about rate limiting never trips it.
	LoginLimit  int
	LoginWindow time.Duration
}

// New starts the service.
func New(t *testing.T, opts HarnessOptions) *Harness {
	t.Helper()

	st := Store(t)
	client := RedisClient(t)

	h := &Harness{
		Store:  st,
		Redis:  client,
		Secret: []byte("a-test-secret-that-is-at-least-32-bytes-long"),
	}

	if opts.AccessTTL == 0 {
		opts.AccessTTL = 15 * time.Minute
	}

	if opts.RefreshTTL == 0 {
		opts.RefreshTTL = 30 * 24 * time.Hour
	}

	if opts.LoginLimit == 0 {
		opts.LoginLimit = 1000
	}

	if opts.LoginWindow == 0 {
		opts.LoginWindow = time.Minute
	}

	limiter, err := ratelimit.New(client, ratelimit.Options{
		Limit:  opts.LoginLimit,
		Window: opts.LoginWindow,
		Prefix: "login",
	})
	if err != nil {
		t.Fatalf("build limiter: %v", err)
	}

	srv := api.New(api.Options{
		Store:   st,
		Limiter: limiter,
		// Discard, not stderr: a passing test prints nothing, and a handler logging from a goroutine after
		// the test ends is a data race the detector finds.
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Secret:     h.Secret,
		AccessTTL:  opts.AccessTTL,
		RefreshTTL: opts.RefreshTTL,
		// bcrypt's minimum. At the production cost of 12 a suite with thirty registrations spends eight
		// seconds hashing, and the pressure to "fix" that lands on the production constant.
		HashCost: auth.TestCost,
	})

	h.Server = httptest.NewServer(srv.Routes())
	t.Cleanup(h.Server.Close)

	return h
}

// Response is a finished exchange.
//
// Bytes rather than an open *http.Response, so no test can leak a connection and there is no obligation to
// hand around. The url-shortener capstone started with the other shape and the linter found six call sites
// that forgot to close.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body.
func (r *Response) JSON(t *testing.T, v any) {
	t.Helper()

	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %d response %q: %v", r.Status, r.Body, err)
	}
}

// Do sends a request and reads the whole response.
func (h *Harness) Do(t *testing.T, method, path, token string, body any) *Response {
	t.Helper()

	return h.DoWith(t, method, path, body, func(req *http.Request) {
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	})
}

// DoWith is Do with the request in hand.
func (h *Harness) DoWith(t *testing.T, method, path string, body any, customise ...func(*http.Request)) *Response {
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

	resp, err := http.DefaultClient.Do(req)
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

// Tokens is a login's result.
type Tokens struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
}

// Register creates a user and returns both tokens.
func (h *Harness) Register(t *testing.T, email, password string) Tokens {
	t.Helper()

	resp := h.Do(t, http.MethodPost, "/api/v1/register", "", map[string]string{
		"email": email, "password": password,
	})

	if resp.Status != http.StatusCreated {
		t.Fatalf("register %s: %d %s", email, resp.Status, resp.Body)
	}

	var tokens Tokens

	resp.JSON(t, &tokens)

	return tokens
}
