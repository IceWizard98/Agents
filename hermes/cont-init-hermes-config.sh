#!/command/with-contenv sh
# Runs at container init (before the gateway), on every boot.
set -eu

CFG=/opt/data/config.yaml
mkdir -p /opt/data

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
