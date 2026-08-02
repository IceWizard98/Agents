set shell := ["bash", "-c"]

# Mirror local Claude Code skills/plugins into the running containers.
# Mirror = deletions propagate: a skill/plugin removed locally is removed in the
# container too (target dir is wiped, then repopulated from local).
# Targets (genuine Claude Code): coding + paperclip (claude_local adapter).
# Hermes isn't Claude Code but scans skill dirs (external_dirs) — gets flattened skills.
# LOCAL recipes: `docker compose up -d` first, target the local daemon.
# REMOTE recipes: rsync --delete to the Coolify host, then wipe+docker cp into the
#   containers found by name pattern + coolify project/environment labels.

skills_src := env_var('HOME') / ".claude/skills"
plugins_src := env_var('HOME') / ".claude/plugins"
# ssh alias of the remote Coolify host (override: AGENTS_SSH_HOST=... just ...)
host := env_var_or_default("AGENTS_SSH_HOST", "free-piva")

# List recipes
default:
    @just --list

# --- stack ---
# Model comes from HERMES_MODEL (.env) — applied at container boot by hermes cont-init.
up:
    docker compose up -d --build
down:
    docker compose down
logs:
    docker compose logs -f

# --- skills: LOCAL (mirror) ---

# Mirror personal skills (~/.claude/skills) into every Claude Code container + hermes.
sync-skills:
    #!/usr/bin/env bash
    set -euo pipefail
    test -d "{{skills_src}}" || { echo "no {{skills_src}}"; exit 1; }
    # svc:dest:owner  (owner '-' = skip chown, e.g. hermes runs as root)
    for t in "coding:/home/app/.claude/skills:app" "paperclip:/paperclip/.claude/skills:node" "hermes:/opt/data/skills:-"; do
      IFS=: read -r svc dest owner <<< "$t"
      docker compose exec -T -u 0 "$svc" sh -c "rm -rf '$dest'; mkdir -p '$dest'"
      docker compose cp "{{skills_src}}/." "$svc:$dest/"
      [ "$owner" = "-" ] || docker compose exec -T -u 0 "$svc" chown -R "$owner:$owner" "$dest"
      echo "skills -> $svc:$dest"
    done

# Mirror plugins into Claude Code containers (native) + flattened skills into hermes.
sync-plugins:
    #!/usr/bin/env bash
    set -euo pipefail
    test -d "{{plugins_src}}" || { echo "no {{plugins_src}}"; exit 1; }
    for t in "coding:/home/app/.claude/plugins:app" "paperclip:/paperclip/.claude/plugins:node"; do
      IFS=: read -r svc dest owner <<< "$t"
      docker compose exec -T -u 0 "$svc" sh -c "rm -rf '$dest'; mkdir -p '$dest'"
      docker compose cp "{{plugins_src}}/." "$svc:$dest/"
      docker compose exec -T -u 0 "$svc" chown -R "$owner:$owner" "$dest"
      echo "plugins -> $svc:$dest (native)"
    done
    just _flatten-plugins
    docker compose exec -T hermes sh -c 'rm -rf /opt/data/plugin-skills; mkdir -p /opt/data/plugin-skills'
    docker compose cp ".skills-dist/." hermes:/opt/data/plugin-skills/
    rm -rf .skills-dist
    echo "plugins -> hermes:/opt/data/plugin-skills (flattened)"

# Everything (local)
sync-all: sync-skills sync-plugins

# --- skills: REMOTE (Coolify host over SSH, mirror) ---

# Mirror personal skills to the remote Claude Code containers + hermes.
# Usage: just sync-skills-remote [project=agents] [environment=production]
sync-skills-remote project="agents" environment="production":
    #!/usr/bin/env bash
    set -euo pipefail
    test -d "{{skills_src}}" || { echo "no {{skills_src}}"; exit 1; }
    rsync -az --delete --rsync-path="mkdir -p /tmp/agents-skills && rsync" "{{skills_src}}/" "{{host}}:/tmp/agents-skills/"
    for t in "coding:/home/app/.claude/skills:app" "paperclip:/paperclip/.claude/skills:node" "hermes:/opt/data/skills:-"; do
      IFS=: read -r svc dest owner <<< "$t"
      cid=$(just _remote-id "$svc" "{{project}}" "{{environment}}")
      ssh "{{host}}" "docker exec -u 0 $cid sh -c 'rm -rf $dest; mkdir -p $dest' && docker cp /tmp/agents-skills/. $cid:$dest/"
      [ "$owner" = "-" ] || ssh "{{host}}" "docker exec -u 0 $cid chown -R $owner:$owner $dest"
      echo "skills -> $svc ($cid):$dest"
    done
    ssh "{{host}}" "rm -rf /tmp/agents-skills"

# Mirror plugins to the remote Claude Code containers + flattened skills to hermes.
# Usage: just sync-plugins-remote [project=agents] [environment=production]
sync-plugins-remote project="agents" environment="production":
    #!/usr/bin/env bash
    set -euo pipefail
    test -d "{{plugins_src}}" || { echo "no {{plugins_src}}"; exit 1; }
    rsync -az --delete --rsync-path="mkdir -p /tmp/agents-plugins && rsync" "{{plugins_src}}/" "{{host}}:/tmp/agents-plugins/"
    for t in "coding:/home/app/.claude/plugins:app" "paperclip:/paperclip/.claude/plugins:node"; do
      IFS=: read -r svc dest owner <<< "$t"
      cid=$(just _remote-id "$svc" "{{project}}" "{{environment}}")
      ssh "{{host}}" "docker exec -u 0 $cid sh -c 'rm -rf $dest; mkdir -p $dest' && docker cp /tmp/agents-plugins/. $cid:$dest/ && docker exec -u 0 $cid chown -R $owner:$owner $dest"
      echo "plugins -> $svc ($cid):$dest (native)"
    done
    just _flatten-plugins
    rsync -az --delete --rsync-path="mkdir -p /tmp/agents-pskills && rsync" ".skills-dist/" "{{host}}:/tmp/agents-pskills/"
    rm -rf .skills-dist
    hid=$(just _remote-id hermes "{{project}}" "{{environment}}")
    ssh "{{host}}" "docker exec $hid sh -c 'rm -rf /opt/data/plugin-skills; mkdir -p /opt/data/plugin-skills' && docker cp /tmp/agents-pskills/. $hid:/opt/data/plugin-skills/"
    ssh "{{host}}" "rm -rf /tmp/agents-plugins /tmp/agents-pskills"
    echo "plugins -> hermes ($hid):/opt/data/plugin-skills (flattened)"

# Everything (remote)
sync-all-remote project="agents" environment="production": (sync-skills-remote project environment) (sync-plugins-remote project environment)

# --- helpers ---

# Print the ID of a running container on the remote host, matched by name pattern + Coolify labels.
_remote-id pattern project environment:
    #!/usr/bin/env bash
    set -euo pipefail
    # project/environment get interpolated into a single-quoted string inside the
    # remote SSH command below — a value containing ' would break out of that
    # quoting and inject shell commands on the remote host. Restrict to the
    # charset Coolify actually uses for these labels.
    for v in "{{project}}" "{{environment}}"; do
      [[ "$v" =~ ^[a-zA-Z0-9_-]+$ ]] || { echo "invalid project/environment '$v' (allowed: [a-zA-Z0-9_-]+)" >&2; exit 1; }
    done
    id=$(ssh "{{host}}" "docker ps --no-trunc \
        --filter 'label=coolify.projectName={{project}}' \
        --filter 'label=coolify.environmentName={{environment}}'" \
        | grep -i "{{pattern}}" | awk '{print $1}' | head -1)
    [ -n "$id" ] || { echo "no container matching '{{pattern}}' on {{host}} ({{project}}/{{environment}})" >&2; exit 1; }
    echo "$id"

# Build .skills-dist/ from plugin SKILL.md dirs (used by sync-plugins*).
_flatten-plugins:
    #!/usr/bin/env bash
    set -euo pipefail
    rm -rf .skills-dist && mkdir -p .skills-dist
    find "{{plugins_src}}" -name SKILL.md -not -path '*/cache/*' | while read -r f; do
        d=$(dirname "$f"); cp -R "$d" ".skills-dist/$(basename "$d")"
    done

# Show what would be pushed
skills-list:
    @echo "personal skills ({{skills_src}}):"; ls -1 "{{skills_src}}" 2>/dev/null || echo "  (none)"
    @echo "plugin skills ({{plugins_src}}):"; find "{{plugins_src}}" -name SKILL.md -not -path '*/cache/*' 2>/dev/null | sed 's|/SKILL.md||;s|.*/||' | sort -u || echo "  (none)"
