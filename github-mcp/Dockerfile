# Official GitHub MCP server (self-hosted) exposed over streamable-HTTP to hermes.
# The official image speaks stdio only: take the binary and bridge it with supergateway.
FROM ghcr.io/github/github-mcp-server AS ghmcp

FROM node:22-bookworm-slim
COPY --from=ghmcp /server/github-mcp-server /usr/local/bin/github-mcp-server
RUN npm install -g supergateway@3.4.3 && useradd -m -u 1001 app
USER app
ENV PORT=9400
EXPOSE 9400
# Readiness: supergateway listening on the MCP port.
HEALTHCHECK --interval=5s --timeout=3s --start-period=15s --retries=5 \
  CMD node -e "require('net').connect(Number(process.env.PORT||9400),'127.0.0.1').on('connect',()=>process.exit(0)).on('error',()=>process.exit(1))"
# GITHUB_PERSONAL_ACCESS_TOKEN and GITHUB_TOOLSETS passed via env from compose.
# --stateful: without it supergateway respawns a fresh github-mcp-server child
# process per request instead of reusing one session — leaks OS threads until
# the container's process ulimit is hit and the Go runtime crashes fatally
# (newosproc). Same bug class already fixed on `coding` (see its entrypoint.sh).
ENTRYPOINT ["sh", "-c", "exec supergateway --stdio 'github-mcp-server stdio' --outputTransport streamableHttp --stateful --port ${PORT:-9400}"]
