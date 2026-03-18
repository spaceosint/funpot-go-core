# syntax=docker/dockerfile:1.7

FROM golang:1.23.0-bookworm AS build
WORKDIR /app

# deps layer cache
COPY go.mod go.sum ./
RUN go mod download

# source
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VCS_REF=""
ARG BUILD_DATE=""

# build
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/funpot ./cmd/server


FROM debian:bookworm-slim AS runtime

ARG VCS_REF=""
ARG BUILD_DATE=""

LABEL org.opencontainers.image.title="funpot" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}"

ENV DEBIAN_FRONTEND=noninteractive \
    PATH="/opt/venv/bin:${PATH}" \
    FUNPOT_ENV=production \
    FUNPOT_SERVER_ADDRESS=:8080

WORKDIR /app

# Устанавливаем:
# - certs
# - python + venv для streamlink
# - ffmpeg
# - tini для корректной работы с subprocess/signal handling
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    python3 \
    python3-venv \
    python3-pip \
    ffmpeg \
    tini \
    && rm -rf /var/lib/apt/lists/*

# Отдельное virtualenv под Streamlink
RUN python3 -m venv /opt/venv \
    && /opt/venv/bin/pip install --no-cache-dir --upgrade pip \
    && /opt/venv/bin/pip install --no-cache-dir "streamlink==8.2.1" \
    && /opt/venv/bin/streamlink --version \
    && ffmpeg -version

# Непривилегированный пользователь
RUN groupadd -g 10001 appuser \
    && useradd -r -u 10001 -g appuser -d /app -s /usr/sbin/nologin appuser

# Бинарник приложения
COPY --from=build /out/funpot /usr/local/bin/funpot

# Если у вас есть дополнительные файлы, раскомментируйте:
# COPY --from=build /app/migrations /app/migrations
# COPY --from=build /app/configs /app/configs
# COPY --from=build /app/static /app/static

RUN chown -R appuser:appuser /app /usr/local/bin/funpot

USER appuser

EXPOSE 8080

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/funpot"]
