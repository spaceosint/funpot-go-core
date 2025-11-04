# syntax=docker/dockerfile:1

FROM golang:1.23.3-bookworm AS build
WORKDIR /app

# Leverage caching by copying go.mod and go.sum first
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application source
COPY . .

# Build the server binary for Linux
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/funpot ./cmd/server

FROM debian:bookworm-slim AS runtime

RUN useradd --system --uid 10001 --home /app --shell /sbin/nologin funpot && \
    apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /bin/funpot /usr/local/bin/funpot

ENV FUNPOT_ENV=production \
    FUNPOT_SERVER_ADDRESS=:8080

EXPOSE 8080
USER funpot

ENTRYPOINT ["/usr/local/bin/funpot"]
