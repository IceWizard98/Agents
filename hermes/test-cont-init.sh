#!/usr/bin/env bash
# Self-check for cont-init-hermes-config.sh provider selection. No docker: a fake
# `hermes` on PATH records every `config set` call; we assert the right ones fire.
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Fake `hermes`: append `config set <key> <val>` invocations to $CALLS.
mkdir -p "$WORK/bin"
cat > "$WORK/bin/hermes" <<'EOF'
#!/usr/bin/env bash
if [ "$1" = "config" ] && [ "$2" = "set" ]; then echo "$3 $4" >> "$CALLS"; fi
exit 0
EOF
chmod +x "$WORK/bin/hermes"

run() { # run <extra-env...> — CFG pre-created so template cp is skipped
  CALLS="$WORK/calls"; : > "$CALLS"
  : > "$WORK/config.yaml"
  env -i PATH="$WORK/bin:/usr/bin:/bin" CFG="$WORK/config.yaml" CALLS="$CALLS" \
    "$@" sh "$HERE/cont-init-hermes-config.sh"
}
has()  { grep -qxF "$1" "$WORK/calls" || { echo "FAIL: expected call [$1]"; cat "$WORK/calls"; exit 1; }; }
lacks(){ grep -qxF "$1" "$WORK/calls" && { echo "FAIL: unexpected call [$1]"; exit 1; } || true; }

# 1. default (no HERMES_PROVIDER) → claude-max active, gemini NOT touched
run HERMES_MODEL=haiku HERMES_BASE_URL=http://proxy:3456/v1
has "model.default haiku"
has "providers.claude-max.api http://proxy:3456/v1"
has "model.provider claude-max"
lacks "model.provider gemini"

# 2. HERMES_PROVIDER=gemini → gemini active, model.default overridden to a gemini id
run HERMES_MODEL=haiku HERMES_BASE_URL=http://proxy:3456/v1 \
    HERMES_PROVIDER=gemini GEMINI_API_KEY=k
has "providers.gemini.api https://generativelanguage.googleapis.com/v1beta/openai/"
has "providers.gemini.key_env GEMINI_API_KEY"
has "model.default gemini-2.5-flash"
has "model.provider gemini"
lacks "model.provider claude-max"

# 3. gemini with custom GEMINI_MODEL/GEMINI_BASE_URL overrides
run HERMES_PROVIDER=gemini GEMINI_API_KEY=k \
    GEMINI_MODEL=gemini-2.5-pro GEMINI_BASE_URL=http://custom/v1/
has "model.default gemini-2.5-pro"
has "providers.gemini.api http://custom/v1/"

# 4. claude-max selected but no endpoint → don't claim the provider
run HERMES_PROVIDER=claude-max HERMES_MODEL=haiku
lacks "model.provider claude-max"

echo "PASS"
