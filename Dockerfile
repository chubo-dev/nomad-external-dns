FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/nomad-external-dns.bin ./cmd/

FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=build /out/nomad-external-dns.bin /usr/local/bin/nomad-external-dns.bin

ENTRYPOINT ["/usr/local/bin/nomad-external-dns.bin"]
