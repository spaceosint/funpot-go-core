#!/usr/bin/env sh
set -eu

if [ "${FUNPOT_RUN_MIGRATIONS_ON_STARTUP:-true}" = "true" ]; then
  if [ -z "${FUNPOT_DATABASE_URL:-}" ]; then
    if [ -n "${FUNPOT_DATABASE_USER:-}" ] && [ -n "${FUNPOT_DATABASE_PASSWORD:-}" ] && [ -n "${FUNPOT_DATABASE_HOST:-}" ] && [ -n "${FUNPOT_DATABASE_NAME:-}" ]; then
      FUNPOT_DATABASE_URL="postgres://${FUNPOT_DATABASE_USER}:${FUNPOT_DATABASE_PASSWORD}@${FUNPOT_DATABASE_HOST}:${FUNPOT_DATABASE_PORT:-5432}/${FUNPOT_DATABASE_NAME}?sslmode=${FUNPOT_DATABASE_SSLMODE:-disable}"
      export FUNPOT_DATABASE_URL
    fi
  fi

  if [ -n "${FUNPOT_DATABASE_URL:-}" ]; then
    migrate -path /app/migrations -database "${FUNPOT_DATABASE_URL}" up
  else
    echo "[entrypoint] FUNPOT_DATABASE_URL not provided; skipping migrations" >&2
  fi
fi

exec /usr/local/bin/funpot
