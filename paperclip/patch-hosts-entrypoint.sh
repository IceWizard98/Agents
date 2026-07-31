#!/bin/sh
set -e

# Pins PAPERCLIP_PUBLIC_URL's hostname to loopback in /etc/hosts so paperclip's own
# self-calls (e.g. smoke-lab) resolve locally instead of leaving the box and
# hairpinning back through an edge proxy (e.g. Cloudflare Access), which blocks or
# times out non-browser requests. Reuses PAPERCLIP_PUBLIC_URL — the same var that
# tells paperclip which origin is valid — for both purposes.
if [ -n "$PAPERCLIP_PUBLIC_URL" ]; then
  host="${PAPERCLIP_PUBLIC_URL#*://}"
  host="${host%%/*}"
  host="${host%%:*}"
  if [ -n "$host" ] && [ "$host" != "localhost" ] && [ "$host" != "127.0.0.1" ]; then
    if ! grep -qE "(^|[[:space:]])${host}([[:space:]]|$)" /etc/hosts; then
      echo "127.0.0.1 ${host}" >> /etc/hosts
    fi
  fi
fi

exec docker-entrypoint.sh "$@"
