#!/usr/bin/env bash
set -euo pipefail

# Client credentials from env (JIRA_URL / JIRA_EMAIL / JIRA_API_TOKEN).
# mcp-atlassian reads these variables; Jira only (Confluence disabled).
exec uvx mcp-atlassian \
  --transport streamable-http \
  --host 0.0.0.0 --port 9200 \
  --jira-url "$JIRA_URL" \
  --jira-username "$JIRA_EMAIL" \
  --jira-token "$JIRA_API_TOKEN"
