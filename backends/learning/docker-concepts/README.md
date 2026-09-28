# docker-concepts

Seven Dockerfiles for the same Go service, built and run by a test suite that measures what each one costs and what
each one breaks. 1,731 lines.

The Python mirror is `backends/learning/docker-concepts`, which covers multi-stage builds, security, debugging, a
reverse proxy and CI. The Go story is shorter and has a bigger payoff, because a static Go binary can ship in an
image with literally nothing else in it, and a Python service cannot.

## Running it

```sh
go test ./...
```

The tests build every Dockerfile and run the containers, so they need a working Docker daemon and skip without one.
A cold run is about three minutes (most of it pulling `golang:1.27` and `gcr.io/distroless`), a warm one is 33
seconds.

```sh
docker compose up --build
curl localhost:8081/buildinfo
```

## The measurements

| Dockerfile | final base | size | layers | user |
| --- | --- | --- | --- | --- |
| `01-naive` | `golang:1.27` | **1.01 GB** | 10 | root |
| `07-cgo` | `debian:12-slim` | 115 MB | 8 | 10001 |
| `02-multistage` | `alpine:3.21` | 16.4 MB | 4 | app |
| `03-distroless` | `distroless/static:nonroot` | **8.8 MB** | 3 | 65532 |
| `04-scratch` | `scratch` + certs + tzdata | 7.53 MB | 3 | 10001 |
| `05-scratch-broken` | `scratch` | 6.68 MB | 1 | 10001 |

**The naive image is 115x the distroless one.** It ships a Go compiler, a full Debian userland and the source code
to production.

**CGO costs 13x**, because a dynamically linked binary needs a libc and therefore an image with a userland.

**scratch saves 1.3 MB over distroless**, and that 1.3 MB is the CA certificates and the time zone database. That
gap is the whole argument for distroless: a megabyte and a half in exchange for not having to remember to copy them
and not having to explain to the next person why HTTPS stopped working.

These come from `docker image inspect`, which reports the UNCOMPRESSED size on disk. A registry stores compressed
layers, so a pull is smaller, usually by about half for anything with text in it. Both numbers are legitimate and
they answer different questions: this one is disk on the node, the compressed one is bytes over the network on a
cold start.

## What an empty scratch image actually breaks

This is the test that surprised me. `05-scratch-broken` copies in the binary and nothing else.

**The server starts and serves traffic.** A static Go binary needs no files at all: no libc, no `/etc/passwd`, no
`/tmp`. So an empty scratch image is not broken, which is exactly what makes it dangerous.

What it gets wrong:

| | with certs and tzdata | with nothing |
| --- | --- | --- |
| outbound HTTPS | `200` | `x509: certificate signed by unknown authority` |
| `time.LoadLocation("Europe/London")` | `BST`, `+01:00` | `unknown time zone Europe/London` |
| `docker exec sh` | fails (no shell either way) | fails |

The certificate error is the nasty one, because it names the remote server's certificate and the problem is in
your image. Every host fails identically, which reads like an outage somewhere else.

The time zone case turned out better than I expected: `LoadLocation` returns an ERROR rather than silently
defaulting to UTC. I had written the comment the other way round and the test corrected it.

## Signal handling, measured

`docker stop` sends SIGTERM, waits out the grace period, then sends SIGKILL.

| | form | exit after | code |
| --- | --- | --- | --- |
| `03-distroless` | `ENTRYPOINT ["/server"]` | **0.4s** | 0 |
| `01-naive` | `CMD /app/server` | **10.2s** | 137 |

Shell form becomes `["/bin/sh","-c","/app/server"]`, so `/bin/sh` is PID 1 and the server is its child. A shell
does not forward SIGTERM. So the container waits out the full grace period and is killed, every in-flight request
is dropped, and the only symptom is that deploys are slow.

That is the entire difference between `CMD ["/app/server"]` and `CMD /app/server`, and it is invisible until
something has to stop gracefully.

## Distroless has no shell, which is the point and the cost

```
exec sh              executable file not found in $PATH
exec /bin/sh -c id   stat /bin/sh: no such file or directory
exec ls /            executable file not found in $PATH
exec cat /etc/passwd executable file not found in $PATH
```

An attacker with remote code execution has nothing to run and no way to fetch a payload. So does the operator:
debugging is `docker cp`, a sidecar sharing the filesystem, or the `:debug` tag, which puts busybox back and gives
up the property.

The `:nonroot` tag sets the user to **65532** in the image, so the container cannot run as root even by accident.
The alternative is a `USER` line, and the tag is one fewer thing to forget.

## `-X` on a variable that does not exist is not an error

`-ldflags="-X main.version=1.2.3"` patches a string variable at link time. Get the path wrong and the build
SUCCEEDS, the variable keeps its default, and **the other flags still work**:

```
with -X main.Version (capital V): version=unknown commit=abc1234
```

One typo produces one wrong field in a health endpoint and nothing else, which is why `main.go` defaults these to
`"unknown"` rather than `""`. A binary built by the wrong pipeline says so instead of looking like a field nobody
filled in.

Three more things about `-X`:

- it only works on a `var`, not a `const`, because the compiler has already folded a const into every use
- the path is the full package path plus the variable name, so `main.version` works only in package main and
  anything else needs `github.com/you/repo/internal/build.Version`
- `debug.ReadBuildInfo` reports what the build actually did, including `CGO_ENABLED`, and `go version -m ./binary`
  reads the same data from outside a running process

## Layer caching is about ordering

```
naive                          staged
0  FROM golang:1.27            0  FROM golang:1.27 AS build
1  WORKDIR /app                1  WORKDIR /src
2  COPY . .                    2  COPY go.mod go.sum* ./
3  RUN go build ...            3  RUN go mod download
                               4  COPY . .
                               ...
                               7  RUN go build ...
```

Docker invalidates a layer when its inputs change. `COPY . .` changes on every commit, so everything after it
reruns on every build. `go.mod` changes rarely, so copying it separately means `go mod download` reruns only when
a dependency changes.

Two extra lines, and it is the biggest build-time win in a Go Dockerfile.

`06-cached.Dockerfile` goes further with BuildKit cache mounts, which persist the module cache and the build cache
between builds and are NOT part of the image. So a rebuild after a one-line change recompiles only that package.
The cost: the cache lives on the builder, so a CI runner with a fresh builder gets nothing unless the cache is
exported, which is what `--cache-from type=gha` is for.

Note that the test asserts on the INSTRUCTION ORDER rather than on a rebuild time. Timing a rebuild here would be
timing the Docker cache, which every other test in the package shares, so it would not be a controlled experiment.

## The runtime settings a container needs and Go does not read

**`GOMEMLIMIT`.** The Go runtime does not read the cgroup memory limit. Without it, a service in a 128 MB
container targets the HOST's memory, grows past the limit, and the OOM killer arrives before the GC does. The
symptom is a container restarting with exit code 137 and no Go error anywhere.

Set it slightly below the container limit, because it is a target for the HEAP and does not count the runtime's own
overhead. 100 MiB against a 128 MB limit.

**`GOMAXPROCS`.** Go 1.25 made the runtime cgroup-aware for CPU, so this is no longer needed on a current
toolchain. Before that, a service limited to 0.5 CPU on a 64-core node started 64 OS threads and spent its time on
scheduler contention. It is still in every guide, which is why the compose file mentions it and leaves it commented
out.

## The compose file's less obvious lines

**`healthcheck: disable: true`**, with an explanation. A distroless image has no `curl` and no `wget`, so a
`HEALTHCHECK` has to run the server's own binary with a flag, which is why many services grow a `-healthcheck`
mode. This one does not have one, so the check is off and the comment says so rather than pretending. Kubernetes
does the HTTP GET itself and needs nothing in the image.

**`stop_grace_period: 20s`** against the server's own 10-second shutdown timeout. Ten and ten is exactly the wrong
pair: they race, and the runtime kills a server that was shutting down correctly.

**`read_only: true`** plus a `tmpfs` for `/tmp`. A Go binary writes nothing else, so this is close to free and it
stops an attacker with RCE from dropping a file anywhere.

**`cap_drop: [ALL]`.** A process listening on 8080 needs no capabilities. `NET_BIND_SERVICE` is only required below
port 1024, which is the reason a container should never listen on 80.

**`init: false`**, with the reasoning. A Go server that never forks has no zombies to reap, so an init process is
unnecessary. It IS necessary for anything that shells out, which is most Python and Node services, and it is
cargo-culted into every compose file regardless.

## The `.dockerignore`

The build context is tarred and uploaded before the first instruction runs, so everything in it costs time on every
build and, for anything secret, risks ending up in a layer.

`.git` is the big one: on a repo with history it can be larger than the source and nothing in a Go build needs it.
The exception is `go build -buildvcs`, which reads the commit from it, and this module passes the commit as a build
ARG instead.

The Dockerfiles themselves are ignored too, so editing one does not invalidate the others' build context.

## Things worth stealing from here

- `internal/dockertest`: builds and runs images by shelling out to the CLI, because the SDK's answer to "build an
  image" is "produce a tar of the context yourself" and shelling out tests the same thing the reader will run.
- `Container.Stop`: sends SIGTERM through `docker stop` and reports how long the container took and what code it
  exited with, which is the only way to test signal handling.
- `dockertest.FormatBytes`: decimal units, because `docker images` reports 8.8MB for 8,800,000 bytes and mixing
  decimal with binary makes a comparison against the CLI look 5% wrong.
- `cmd/api`'s `/buildinfo`, `/dns`, `/tls` and `/tz`: four endpoints that report what the IMAGE has, so a test can
  measure an absence from inside the container rather than inferring it.
