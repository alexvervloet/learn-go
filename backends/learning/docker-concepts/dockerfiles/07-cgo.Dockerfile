# A CGO build, to show what it costs and what it breaks.
#
# CGO is on by default. Most Go programs do not need it, and the ones that do are usually using a SQLite driver,
# a crypto library, or the system's DNS resolver deliberately.
#
# With CGO on:
#
#   the binary is DYNAMICALLY LINKED against libc, so it runs only on an image with a compatible one
#   the build needs a C toolchain, so the build image gets bigger and slower
#   cross-compiling needs a cross C toolchain, which is the hardest part of shipping Go for two architectures
#
# The final stage here is debian-slim rather than scratch, because a glibc-linked binary cannot run on scratch at
# all. The error is "no such file or directory" for the binary, which is there: the missing file is the dynamic
# linker.

# golang:1.27-bookworm, not golang:1.27, so the build and the final stage are the same Debian release. A
# glibc-linked binary needs the glibc it was BUILT against or newer. golang:1.27 is Debian trixie (glibc 2.41)
# and debian:12-slim is bookworm (glibc 2.36), which is how the first version of this file was paired. It
# happened to start, because this binary uses only old glibc symbols. The first call into anything added after
# 2.36 would fail at startup with "version `GLIBC_2.38' not found". Build on the runtime's release.
FROM golang:1.27-bookworm AS build

WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# CGO_ENABLED=1, explicitly, because it is the default and being explicit is the point of the comparison.
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/api

FROM debian:12-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*

RUN useradd --uid 10001 --create-home app

COPY --from=build /out/server /usr/local/bin/server

USER app

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/server"]
