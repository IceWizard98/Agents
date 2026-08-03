#!/usr/bin/env bash
set -euo pipefail

# Google Workspace MCP (mail/calendar/drive) on :9000 — streamable HTTP.
# workspace-mcp reads host/port/base-uri from ENV, NOT from --host/--port flags
# (those crash with "unrecognized arguments"). See `workspace-mcp --help`.
export GOOGLE_MCP_CREDENTIALS_DIR=/data                       # OAuth token cache (persisted volume)
export WORKSPACE_MCP_HOST=0.0.0.0                             # bind all ifaces (default 127.0.0.1 is unreachable from Hermes)
export WORKSPACE_MCP_PORT=9000                                # Hermes reaches http://mcp-google:9000/mcp
export WORKSPACE_MCP_BASE_URI="${WORKSPACE_MCP_BASE_URI:-http://localhost}"  # OAuth redirect host; prod: set to public https URL
export OAUTHLIB_INSECURE_TRANSPORT=1                          # allow http OAuth redirect (single-user, self-hosted)

# Behind a TLS reverse proxy (Coolify/Cloudflare terminate on 443), workspace-mcp would
# otherwise build the OAuth redirect as "{BASE_URI}:{WORKSPACE_MCP_PORT}/oauth2callback"
# (auth/oauth_config.py:_get_redirect_uri) — leaking the internal port 9000 into the
# public URL and causing Google redirect_uri_mismatch. When the base is public https,
# pin the portless redirect explicitly (GOOGLE_OAUTH_REDIRECT_URI wins over base:port)
# and set the external URL for other generated links. NB: Google Cloud Console must list
# this exact URI, and any Cloudflare Access in front must bypass /oauth2callback.
case "$WORKSPACE_MCP_BASE_URI" in
  https://*)
    export WORKSPACE_EXTERNAL_URL="${WORKSPACE_EXTERNAL_URL:-${WORKSPACE_MCP_BASE_URI%/}}"
    export GOOGLE_OAUTH_REDIRECT_URI="${GOOGLE_OAUTH_REDIRECT_URI:-${WORKSPACE_MCP_BASE_URI%/}/oauth2callback}"
    ;;
esac

# Single-user mode: one Google account, no per-request OAuth session handshake.
# USER_GOOGLE_EMAIL (optional) pins the default account for cached credentials.
exec uvx workspace-mcp \
  --transport streamable-http \
  --single-user \
  --tools gmail calendar drive
