# Official GitHub MCP server (self-hosted) exposed over streamable-HTTP to hermes.
# The official image speaks stdio only: take the binary and bridge it with supergateway.
# Pinned by digest: carries GITHUB_TOKEN with write toolsets — bump deliberately.
FROM ghcr.io/github/github-mcp-server@sha256:d5a18c04b92714c309eb46a2305087e91a4dbd80420f6e462656699f95093520 AS ghmcp

FROM node:22-bookworm-slim
COPY --from=ghmcp /server/github-mcp-server /usr/local/bin/github-mcp-server
# ca-certificates: the github-mcp-server is a static Go binary that reads the SYSTEM
# CA store (/etc/ssl/certs) to verify api.github.com's TLS cert. node:22-bookworm-slim
# ships without it (npm uses Node's bundled CAs, so the build still works) — without
# this every GitHub call fails "x509: certificate signed by unknown authority".
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    npm install -g supergateway@3.4.3 && useradd -m -u 1001 app
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
