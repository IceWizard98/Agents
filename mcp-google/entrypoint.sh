#!/usr/bin/env bash
set -euo pipefail

# Google Workspace MCP (mail/calendar/drive) on :9000 — streamable HTTP.
# NB: verify package name/flags on first deploy; here: workspace-mcp.
# Google OAuth: GOOGLE_OAUTH_CLIENT_ID / GOOGLE_OAUTH_CLIENT_SECRET in env.
# Token cache persisted in /data (volume).
export GOOGLE_MCP_CREDENTIALS_DIR=/data
exec uvx workspace-mcp --transport streamable-http --host 0.0.0.0 --port 9000
