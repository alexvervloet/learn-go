# The same multi-stage build with BuildKit cache mounts, which is the fastest version.
#
# A cache mount is a directory that persists between builds and is NOT part of the image. So the Go module cache
# and the build cache survive a `docker build` that invalidated every layer, and a rebuild after a one-line source
# change recompiles only that package rather than the whole dependency tree.
#
# The cost: it needs BuildKit (the default since Docker 23) and the cache lives on the builder, so a CI runner
# with a fresh builder gets nothing from it unless the cache is exported. That is what
# `--cache-from type=gha` is for in GitHub Actions.

# syntax=docker/dockerfile:1.7
#
# The syntax directive has to be the FIRST line of the file to take effect, and it pins the Dockerfile frontend
# rather than the base image. Without it, cache mounts work on a recent Docker and fail on an older one with a
# parse error, which is a confusing way to learn about frontends.

FROM golang:1.27 AS build

WORKDIR /src

COPY go.mod go.sum* ./

# Two cache mounts:
#
#   /go/pkg/mod    the module cache: downloaded dependencies
#   /root/.cache/go-build  the build cache: compiled packages
#
# sharing=locked, because two concurrent builds writing the same module cache corrupt it. The default is
# `shared`, which is right for a read-mostly cache and wrong for this one.
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/server /server

EXPOSE 8080

ENTRYPOINT ["/server"]
