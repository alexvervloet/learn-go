# Two stages: a builder with the toolchain, and a runtime with the binary and nothing else.
#
# The reasoning behind every line here is worked through in backends/learning/docker-concepts, where seven
# Dockerfiles are built and their sizes measured. This is the one that wins.

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------
FROM golang:1.27-alpine AS build

WORKDIR /src

# go.mod and go.sum first, alone.
#
# Docker caches each layer against the files it copied. Copying the whole tree before `go mod download` means
# every source change invalidates the download, and the dependencies are fetched again on every build. Copying
# the two manifests first means the download layer is reused until a dependency actually changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 for a static binary.
#
# The default on Alpine links against musl, and the resulting binary does NOT run on a distroless or scratch
# image built on glibc, or the reverse. The failure is `no such file or directory` on a file that is plainly
# there, which is the dynamic loader failing to find libc and reporting it terribly.
#
# It also switches net and os/user to their pure Go implementations, which is why the image below needs no
# nsswitch.conf.
#
# -trimpath removes the build machine's directory from every path in the binary, which makes the build
# reproducible and stops a panic printing /home/whoever/go/src/...
#
# -s -w strips the symbol table and DWARF. It is most of the size of a Go binary and it costs a readable stack
# trace in a debugger, not in a panic: runtime panics still symbolise.
ARG VERSION=dev
ARG COMMIT=unknown

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w" \
      -o /out/worker ./cmd/worker

# ---------------------------------------------------------------------------
# Runtime
# ---------------------------------------------------------------------------
#
# distroless/static rather than scratch.
#
# scratch is smaller by about a megabyte and is missing three things this service needs: CA certificates, so
# every outbound TLS connection fails with `x509: certificate signed by unknown authority`; /etc/passwd, so
# USER by name does not resolve; and /tmp.
#
# distroless/static has those and no shell, no package manager and no coreutils, which is the security
# property that matters. An attacker with code execution in a container with a shell has a toolbox; here they
# have the one binary.
FROM gcr.io/distroless/static-debian12:nonroot

# nonroot is uid 65532, already in the base image's /etc/passwd.
#
# Running as root in a container is the default and is wrong: a container escape starts as whoever the process
# is. The numeric form works even if something strips /etc/passwd.
USER 65532:65532

COPY --from=build /out/api /api
COPY --from=build /out/worker /worker

EXPOSE 8080

# Exec form, not shell form.
#
# Shell form (`CMD /api`) runs the binary under `/bin/sh -c`, which makes sh PID 1. sh does not forward
# SIGTERM, so `docker stop` waits the full grace period and then SIGKILLs, and every graceful shutdown in
# cmd/api/main.go does nothing. The exec form makes the binary PID 1 and the signal reaches it.
#
# distroless has no shell at all, so the shell form would simply fail here, which is one more reason to use it.
ENTRYPOINT ["/api"]
