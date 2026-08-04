#!/command/with-contenv sh
# Runs at container init (before the gateway), on every boot.
set -eu

CFG=/opt/data/config.yaml
mkdir -p /opt/data

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

# Model is driven by the HERMES_MODEL env var. On Coolify: change the env var and
# redeploy — this re-applies it on the next boot. Provider stays as in config.
if [ -n "${HERMES_MODEL:-}" ]; then
  hermes config set model.default "$HERMES_MODEL" || \
    echo "[cont-init] warning: could not apply HERMES_MODEL=$HERMES_MODEL"
fi

# Brain via the claude-max-api-proxy. Uses Hermes' native custom-provider mechanism
# (providers.<slug> = an OpenAI-compatible endpoint + key env), NOT model.base_url:
# a bare model.base_url is ignored while model.provider names a built-in provider like
# openrouter. When HERMES_BASE_URL is set we register the `claude-max` provider and
# point model.provider at it; unset it (and pick another provider) to revert.
if [ -n "${HERMES_BASE_URL:-}" ]; then
  hermes config set providers.claude-max.api "$HERMES_BASE_URL" >/dev/null 2>&1 || \
    echo "[cont-init] warning: could not set providers.claude-max.api=$HERMES_BASE_URL"
  hermes config set providers.claude-max.key_env OPENAI_API_KEY >/dev/null 2>&1 || true
  hermes config set model.provider claude-max >/dev/null 2>&1 || true
fi
