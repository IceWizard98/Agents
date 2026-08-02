# coding container: Claude Code CLI exposed as an MCP tool to hermes.
# `claude mcp serve` speaks stdio -> supergateway bridges it to streamable-HTTP :9100.
# No code: just the official Claude Code CLI + git + the bridge (both pinned).
FROM node:22-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      git ca-certificates gosu && \
    npm install -g @anthropic-ai/claude-code@2.1.220 supergateway@3.4.3 && \
    apt-get clean && rm -rf /var/lib/apt/lists/* && \
    useradd -m -u 1001 app && mkdir -p /work && chown app:app /work

COPY --chmod=0755 entrypoint.sh /usr/local/bin/entrypoint.sh
# Stay root here: entrypoint.sh chowns the coding_home volume (mounted at
# /home/app/.claude, may be root-owned — see entrypoint.sh) then drops to
# `app` via gosu before running anything else.
WORKDIR /work
ENV PORT=9100
EXPOSE 9100
# Readiness: supergateway listening on the MCP port.
HEALTHCHECK --interval=5s --timeout=3s --start-period=20s --retries=5 \
  CMD node -e "require('net').connect(Number(process.env.PORT||9100),'127.0.0.1').on('connect',()=>process.exit(0)).on('error',()=>process.exit(1))"
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
