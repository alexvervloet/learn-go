# The Dockerfile everybody writes first, kept so the comparison has a baseline.
#
# It works. It is also about 1.5 GB, rebuilds every dependency on every source change, runs as root, and ships a
# Go compiler and a full Debian userland to production.
#
# Every problem with it is fixed by one of the files after it, and the README has the measured sizes.

FROM golang:1.27

WORKDIR /app

# Copying everything before downloading dependencies is the cache mistake. Docker invalidates a layer when its
# inputs change, and "everything" changes on every commit, so `go mod download` reruns every build.
COPY . .

RUN go build -o /app/server ./cmd/api

EXPOSE 8080

# Shell form, which is the other mistake in this file.
#
# `CMD go run ...` becomes `/bin/sh -c "go run ..."`, so the SHELL is PID 1 and the Go process is its child. A
# shell does not forward SIGTERM, so `docker stop` kills the container after the grace period instead of stopping
# it, in-flight requests are dropped, and the symptom is "our deploys drop connections".
CMD /app/server
