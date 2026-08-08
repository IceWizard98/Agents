#!/command/with-contenv sh
# Runs at container init (before the gateway), on every boot.
set -eu

CFG="${CFG:-/opt/data/config.yaml}"
mkdir -p "$(dirname "$CFG")"

# Il seeder delle bundled-skill gira come utente non-root `hermes`: deve poter
# creare le sottocartelle-categoria sotto il volume persistente. Una
# /opt/data/skills lasciata root-owned da un'immagine vecchia blocca il seeding
# di TUTTE le bundled skill ("Permission denied"). chown non-ricorsivo: non tocca
# le skill utente già presenti, dà solo il permesso di mkdir sulla dir padre.
if [ -d /opt/data/skills ]; then
  chown hermes:hermes /opt/data/skills 2>/dev/null || true
fi

# Seed the versioned template into the data volume on first boot only
# (afterwards Hermes owns/rewrites config.yaml at runtime).
if [ ! -f "$CFG" ]; then
  cp /opt/hermes/template.config.yaml "$CFG"
fi

# Register the mcp-fetch tool (Cloudflare-bypass web fetch) on every boot,
# idempotent. The live config.yaml persists in the volume and is only seeded on
# first boot, so an already-provisioned deployment won't pick it up from the
# template alone — set it explicitly here (same pattern as the providers below).
hermes config set mcp_servers.fetch.url http://mcp-fetch:9002/mcp >/dev/null 2>&1 || \
  echo "[cont-init] warning: could not set mcp_servers.fetch.url"

# Register the mcp-supermemory tool (persistent memory) on every boot, idempotent
# (same reason as fetch above — live config.yaml is only seeded on first boot).
hermes config set mcp_servers.supermemory.url http://mcp-supermemory:9003/mcp >/dev/null 2>&1 || \
  echo "[cont-init] warning: could not set mcp_servers.supermemory.url"

# Register the github-mcp tool (branch/commit/PR) on every boot, idempotent
# (same reason as fetch above — live config.yaml is only seeded on first boot).
hermes config set mcp_servers.github.url http://github-mcp:9400/mcp >/dev/null 2>&1 || \
  echo "[cont-init] warning: could not set mcp_servers.github.url"

# Model is driven by the HERMES_MODEL env var. On Coolify: change the env var and
# redeploy — this re-applies it on the next boot. Provider stays as in config.
if [ -n "${HERMES_MODEL:-}" ]; then
  hermes config set model.default "$HERMES_MODEL" || \
    echo "[cont-init] warning: could not apply HERMES_MODEL=$HERMES_MODEL"
fi

# Two custom providers are registered below (Hermes' native mechanism:
# providers.<slug> = an OpenAI-compatible endpoint + key env). Only ONE is active
# at a time — HERMES_PROVIDER (default: claude-max) picks it and points
# model.provider at it. Registering the inactive one is idempotent and harmless.
PROVIDER="${HERMES_PROVIDER:-claude-max}"

# Brain #1 — claude-max-api-proxy. Registers the `claude-max` provider when its
# endpoint is set. NOT model.base_url: a bare model.base_url is ignored while
# model.provider names a built-in provider like openrouter.
if [ -n "${HERMES_BASE_URL:-}" ]; then
  hermes config set providers.claude-max.api "$HERMES_BASE_URL" >/dev/null 2>&1 || \
    echo "[cont-init] warning: could not set providers.claude-max.api=$HERMES_BASE_URL"
  hermes config set providers.claude-max.key_env OPENAI_API_KEY >/dev/null 2>&1 || true
fi

# Brain #2 — Google Gemini, direct via its OpenAI-compatible endpoint (no proxy:
# an API key CAN be used over raw HTTP, unlike the Claude Max OAuth token). The key
# is sent as `Authorization: Bearer $GEMINI_API_KEY` by the OpenAI-compat provider.
if [ -n "${GEMINI_API_KEY:-}" ]; then
  hermes config set providers.gemini.api \
    "${GEMINI_BASE_URL:-https://generativelanguage.googleapis.com/v1beta/openai/}" >/dev/null 2>&1 || \
    echo "[cont-init] warning: could not set providers.gemini.api"
  hermes config set providers.gemini.key_env GEMINI_API_KEY >/dev/null 2>&1 || true
fi

# Activate the chosen provider. Gemini needs a gemini model id (a bare tier alias
# like `haiku` from HERMES_MODEL would be sent verbatim to Google and rejected), so
# override model.default with GEMINI_MODEL when gemini is active.
case "$PROVIDER" in
  gemini)
    hermes config set model.default "${GEMINI_MODEL:-gemini-2.5-flash}" >/dev/null 2>&1 || true
    hermes config set model.provider gemini >/dev/null 2>&1 || true
    ;;
  claude-max)
    # Only claim claude-max if its endpoint was actually registered above.
    if [ -n "${HERMES_BASE_URL:-}" ]; then
      hermes config set model.provider claude-max >/dev/null 2>&1 || true
    fi
    ;;
  *)
    hermes config set model.provider "$PROVIDER" >/dev/null 2>&1 || \
      echo "[cont-init] warning: unknown HERMES_PROVIDER=$PROVIDER left as-is"
    ;;
esac
