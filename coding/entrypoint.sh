#!/usr/bin/env bash
set -euo pipefail

# coding_home (named volume, mounted at /home/app/.claude) gets root:root
# ownership from Docker on its first-ever mount regardless of the image's
# ownership — verified live: `app` (uid 1001) got EACCES on mkdir under it,
# breaking every claude tool call. Chown every boot so this can't reappear
# no matter the volume's history, then drop root via gosu for everything else.
mkdir -p /home/app/.claude
chown -R app:app /home/app/.claude /work

if [ -n "${GITHUB_TOKEN:-}" ]; then
  gosu app git config --global credential.helper \
    '!f() { echo "username=x-access-token"; echo "password=${GITHUB_TOKEN}"; }; f'
  gosu app git config --global url."https://github.com/".insteadOf "git@github.com:"
fi
gosu app git config --global user.name  "${GIT_AUTHOR_NAME:-agent}"
gosu app git config --global user.email "${GIT_AUTHOR_EMAIL:-agent@users.noreply.github.com}"

exec gosu app supergateway \
  --stdio "claude mcp serve" \
  --outputTransport streamableHttp \
  --stateful \
  --port "${PORT:-9100}"
