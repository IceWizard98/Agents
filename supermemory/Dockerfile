# Self-hosted supermemory server (REST on :6767). Single binary from the
# supermemoryai/supermemory GitHub Releases — embedded graph engine, local
# Xenova embeddings, no external DB. hermes reaches it via the mcp-supermemory
# tool, not directly.
#
# The installer downloads the OS+arch-matched binary, verifies sha256, and (only
# when interactive) prompts for an LLM key. NO_START/NO_PROMPT make it headless;
# LLM + embedding config comes from container env at runtime (see docker-compose).
FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends curl ca-certificates \
	&& rm -rf /var/lib/apt/lists/*

# Pinned: "latest" (0.0.6) ships a Linux binary with the ingestion pipeline broken
# — @rivetkit/rivetkit-wasm isn't bundled, so documents stay "queued", never get
# chunked/embedded, and every search returns empty (upstream issues #1315, #1324).
# 0.0.7-rc.2 fixes it (verified: ingest indexes, v3 search returns scored hits).
# Note: 0.0.7+ requires an LLM key at boot (OPENAI_API_KEY et al.) — the stack sets it.
ARG SUPERMEMORY_VERSION=0.0.7-rc.2
ENV SUPERMEMORY_NO_START=1 \
    SUPERMEMORY_NO_PROMPT=1
RUN curl -fsSL https://supermemory.ai/install | bash -s -- "${SUPERMEMORY_VERSION}"

ENV PATH="/root/.local/bin:${PATH}" \
    PORT=6767 \
    SUPERMEMORY_DATA_DIR=/data
# Persist runtime data (graph engine, embeddings) at SUPERMEMORY_DATA_DIR — a
# separate path from the install dir (/root/.supermemory: binary + env). The
# docker volume mounts at /data, so it must NOT shadow the install dir or the
# binary vanishes on first boot.
RUN mkdir -p /data
EXPOSE 6767
# The installed wrapper sources /root/.supermemory/env then execs the binary;
# runtime docker env (OPENAI_BASE_URL, embedding provider, PORT, data dir) is
# honored on top (the env file sets no OPENAI_* under headless install).
ENTRYPOINT ["supermemory-server"]
