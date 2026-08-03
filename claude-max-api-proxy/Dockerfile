# claude-max-api-proxy: exposes the Claude Max subscription as an OpenAI-compatible
# API for Hermes. Wraps the published `claude-max-api-proxy` npm package, which spawns
# the genuine Claude Code CLI (`claude --print`) as a subprocess — the ALLOWED path for
# the subscription OAuth token (same binary the `coding` container uses), not header
# spoofing. See ../diary.md.
FROM node:22-bookworm-slim
# Pin the CLI to the same version the `coding` container uses (proven headless with
# CLAUDE_CODE_OAUTH_TOKEN). The proxy calls it with --print --output-format stream-json
# --verbose --include-partial-messages --no-session-persistence; those flags must exist
# in this CLI version.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git && \
    npm install -g @anthropic-ai/claude-code@2.1.220 && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY package.json ./
RUN npm install --omit=dev
COPY server.mjs ./

ENV PORT=3456
EXPOSE 3456
# Readiness: HTTP server accepting connections on PORT.
HEALTHCHECK --interval=5s --timeout=3s --start-period=20s --retries=5 \
  CMD node -e "require('net').connect(Number(process.env.PORT||3456),'127.0.0.1').on('connect',()=>process.exit(0)).on('error',()=>process.exit(1))"
CMD ["node", "server.mjs"]
