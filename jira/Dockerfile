# Jira MCP — reuses mcp-atlassian (community), exposed over streamable HTTP on :9200.
# Isolated container: access to the client's Jira only, nothing else.
FROM python:3.12-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    pip install --no-cache-dir uv==0.12.3 && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
EXPOSE 9200
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
