# A scratch image with NOTHING copied in, so the tests can measure what breaks.
#
# This is not a mistake to fix. It is the control: every failure it produces is one that a scratch image has by
# default and that a reader would otherwise have to take on trust.

FROM golang:1.27 AS build

WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/api

FROM scratch

COPY --from=build /out/server /server

EXPOSE 8080

ENTRYPOINT ["/server"]
