# Use the official Go builder image
FROM golang:1.24.6 AS builder

ARG LDFLAGS
ARG GITHUB_TOKEN

# make sure /out exists
RUN mkdir -p /out

WORKDIR /src
COPY go.mod go.sum ./

# Configure git for private modules using build arg
ENV GOPRIVATE=github.com/celestiaorg/*
RUN if [ -n "$GITHUB_TOKEN" ]; then \
        git config --global url."https://${GITHUB_TOKEN}@github.com/".insteadOf "https://github.com/"; \
    fi && \
    go mod download

COPY . .

RUN GOOS=${GOOS} GOARCH=${GOARCH} CGO_ENABLED=0 go build -tags="ledger" -ldflags="${LDFLAGS}" -o /out/celestia-appd ./cmd/celestia-appd \
 && GOOS=${GOOS} GOARCH=${GOARCH} CGO_ENABLED=0 go build -o /out/fibre-load ./tools/fibre-load
