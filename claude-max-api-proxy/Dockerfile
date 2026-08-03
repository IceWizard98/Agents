# claude-max-api-proxy: exposes the Claude Max subscription as an OpenAI-compatible API
# for Hermes, with tool-calling. Drives the genuine Claude Code CLI (`claude --print`) as
# a PLANNER: Hermes' tools become "actions" the model PROPOSES (never executes), which we
# translate to OpenAI tool_calls for Hermes to run. This is the ALLOWED path (real CLI
# binary on CLAUDE_CODE_OAUTH_TOKEN), not a header-spoofed HTTP call. See ../diary.md.
FROM node:22-bookworm-slim
# Same CLI version as the `coding` container (proven headless with CLAUDE_CODE_OAUTH_TOKEN).
# We use --print --exclude-dynamic-system-prompt-sections --system-prompt --model; those
# flags must exist in this version.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git && \
    npm install -g @anthropic-ai/claude-code@2.1.220 && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY package.json lib.mjs server.mjs test.mjs ./
# No runtime deps (bare node:http). Gate the image on the translation unit tests.
RUN node test.mjs

ENV PORT=3456
EXPOSE 3456
HEALTHCHECK --interval=5s --timeout=3s --start-period=20s --retries=5 \
  CMD node -e "require('net').connect(Number(process.env.PORT||3456),'127.0.0.1').on('connect',()=>process.exit(0)).on('error',()=>process.exit(1))"
CMD ["node", "server.mjs"]
