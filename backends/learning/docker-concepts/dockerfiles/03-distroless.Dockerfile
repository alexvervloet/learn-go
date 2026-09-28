# Distroless: the binary, the CA certificates, the time zone database, and nothing else.
#
# No shell, no package manager, no busybox, no libc. Which means:
#
#   an attacker who achieves RCE has no shell to run and no curl to fetch a payload
#   `docker exec -it ... sh` does not work, so debugging is a different workflow
#   a CVE scanner reports almost nothing, because there is almost nothing to report
#
# The last point is the one that gets it adopted and the second is the one that gets it reverted.

FROM golang:1.27 AS build

WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

# -X patches a string variable at link time. The path is the full package path plus the variable name, and
# getting it wrong is SILENT: the build succeeds and the variable keeps its default, which is why main.go
# defaults them to "unknown" rather than "".
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w \
      -X main.version=${VERSION} \
      -X main.commit=${COMMIT} \
      -X main.buildTime=${BUILD_TIME}" \
    -o /out/server ./cmd/api

# ---------------------------------------------------------------------------
# Final stage: distroless static
# ---------------------------------------------------------------------------
#
# `static` rather than `base`: base includes glibc for a CGO binary, and a CGO_ENABLED=0 binary needs none of it.
#
# `:nonroot` rather than `:latest`: the tag sets USER to 65532 in the image, so the container cannot run as root
# even by accident. The alternative is a USER line here, and the tag is one fewer thing to forget.
FROM gcr.io/distroless/static-debian12:nonroot

# The image already has /etc/ssl/certs/ca-certificates.crt and /usr/share/zoneinfo, which is most of the reason
# to use it over scratch.
COPY --from=build /out/server /server

EXPOSE 8080

ENTRYPOINT ["/server"]
