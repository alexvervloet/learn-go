# scratch: literally nothing. The image is the binary.
#
# This is the smallest possible image and it is smaller than distroless by about two megabytes, which is the
# certificates and the zoneinfo. Those two megabytes are the whole difference and they are why distroless is
# usually the better choice.
#
# What a scratch image does NOT have, and what each absence breaks:
#
#   /etc/ssl/certs      every outbound HTTPS request fails with "certificate signed by unknown authority"
#   /usr/share/zoneinfo every time.LoadLocation fails, and time zones silently become UTC
#   /etc/passwd         os/user lookups fail, and a USER directive naming a name rather than a number fails
#   /tmp                anything using os.CreateTemp fails
#   a shell             no exec, no debugging, and no shell-form CMD
#
# This file copies the first two in, which is the version that works. The test suite builds one WITHOUT them to
# measure what breaks.

FROM golang:1.27 AS build

WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/api

# ---------------------------------------------------------------------------
FROM scratch

# The certificates and the zone database, copied from the build image because scratch has nothing to install
# them with. This is what distroless gives you for free.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=build /out/server /server

# A NUMERIC user, because there is no /etc/passwd to resolve a name against. `USER app` in a scratch image fails
# at start with "unable to find user app".
USER 10001:10001

EXPOSE 8080

ENTRYPOINT ["/server"]
