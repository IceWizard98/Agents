#!/usr/bin/env bash
set -euo pipefail

if [ -n "${GITHUB_TOKEN:-}" ]; then
  git config --global credential.helper \
    '!f() { echo "username=x-access-token"; echo "password=${GITHUB_TOKEN}"; }; f'
  git config --global url."https://github.com/".insteadOf "git@github.com:"
fi
git config --global user.name  "${GIT_AUTHOR_NAME:-agent}"
git config --global user.email "${GIT_AUTHOR_EMAIL:-agent@users.noreply.github.com}"

exec supergateway \
  --stdio "claude mcp serve" \
  --outputTransport streamableHttp \
  --stateful \
  --port "${PORT:-9100}"
