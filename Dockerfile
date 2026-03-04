# syntax=docker/dockerfile:1.7

FROM golang:1.24.3 AS build
WORKDIR /src

# (1) deps layer cache: меняется редко → отлично кешируется
COPY go.mod go.sum ./
RUN go mod download

# (2) source
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VCS_REF=""
ARG BUILD_DATE=""

# (3) build (с небольшим локальным кешем компиляции, если BuildKit)
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/funpot ./cmd/server

# distroless: маленький, без shell/apt, и с CA certs внутри :contentReference[oaicite:1]{index=1}
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

WORKDIR /app
COPY --from=build /out/funpot /usr/local/bin/funpot

ENV FUNPOT_ENV=production \
    FUNPOT_SERVER_ADDRESS=:8080

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/funpot"]
