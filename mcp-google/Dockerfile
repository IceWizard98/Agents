# Google Workspace MCP (mail/calendar/drive) via uvx — community package.
FROM python:3.12-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && \
    pip install --no-cache-dir uv && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
EXPOSE 9000
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
