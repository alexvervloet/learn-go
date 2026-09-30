# Multi-stage: build in the toolchain image, ship the binary in a small one.
#
# This is the shape almost every Go Dockerfile should have, and the rest of the files in this directory are
# variations on which base image the final stage uses.

# ---------------------------------------------------------------------------
# Build stage
# ---------------------------------------------------------------------------
FROM golang:1.27 AS build

WORKDIR /src

# Dependencies FIRST, in their own layer.
#
# go.mod and go.sum change rarely and the source changes constantly, so copying them separately means
# `go mod download` reruns only when a dependency changes. That is the single biggest build-time win in a Go
# Dockerfile and it is two extra lines.
COPY go.mod go.sum* ./

RUN go mod download

COPY . .

# CGO_ENABLED=0 is what makes the binary static.
#
# Without it, a build on a glibc image links against glibc and the binary does not run on Alpine (musl) or on
# scratch (nothing). The error is "no such file or directory" for a file that is there, because the missing file
# is the dynamic linker.
#
# GOOS and GOARCH are explicit so a cross-build is a flag rather than a surprise.
ARG TARGETOS=linux
ARG TARGETARCH=amd64

# -trimpath removes the build machine's directory names from the binary, which is a reproducibility fix and a
# small privacy one: without it, every panic prints /home/whoever/go/src/...
#
# -ldflags="-s -w" strips the symbol table and the DWARF debug info. It makes the binary about 25% smaller and
# makes a core dump or a delve session useless, which is the trade. (Not measured in this module.)
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/api

# ---------------------------------------------------------------------------
# Final stage
# ---------------------------------------------------------------------------
FROM alpine:3.21

# ca-certificates, because a scratch or minimal image has none and every outbound HTTPS request fails with
# "x509: certificate signed by unknown authority", which reads like the remote server's problem.
#
# tzdata, because without it time.LoadLocation("Europe/London") returns "unknown time zone", so a service that
# formats a user's local time errors on every request. (An earlier version of this comment said zones silently
# become UTC; the scratch test in this module showed LoadLocation returns an error.)
RUN apk add --no-cache ca-certificates tzdata

# A non-root user, created in the image rather than set with --user at runtime. A container that only runs
# correctly when the operator remembers a flag runs as root the first time somebody forgets.
RUN adduser -D -u 10001 app

COPY --from=build /out/server /usr/local/bin/server

USER app

EXPOSE 8080

# EXEC form, so the binary is PID 1 and receives SIGTERM directly.
ENTRYPOINT ["/usr/local/bin/server"]
