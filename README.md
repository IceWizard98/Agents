# agents

![Docker Compose](https://img.shields.io/badge/Docker_Compose-2496ED?logo=docker&logoColor=white)
![Go](https://img.shields.io/badge/Go-00ADD8?logo=go&logoColor=white)
![Node.js](https://img.shields.io/badge/Node.js-5FA04E?logo=nodedotjs&logoColor=white)
![Claude](https://img.shields.io/badge/Claude_Code-D97757?logo=anthropic&logoColor=white)
![MCP](https://img.shields.io/badge/MCP-000000?logo=modelcontextprotocol&logoColor=white)
![Telegram](https://img.shields.io/badge/Telegram-26A5E4?logo=telegram&logoColor=white)
![Coolify](https://img.shields.io/badge/Coolify-8B5CF6?logo=coolify&logoColor=white)

Multi-container agent system for Coolify, on a shared network (`agents_net`).
Built on **real open-source products**, not reimplementations:

- **[Hermes](https://hermes-agent.nousresearch.com/)** (Nous Research) — the AI
  worker. You chat with it from **Telegram**, it runs tasks and uses tools over MCP.
  Runs on your **Claude Code subscription session** (no API key).
- **[Paperclip](https://github.com/paperclipai/paperclip)** (paperclipai) — the
  **governance/orchestration** layer. Hires Hermes as a worker (built-in
  `hermes_gateway` adapter), assigns recurring tasks, budget and audit.

The two paths coexist:

```
CHAT:      you ──Telegram──▶ hermes ──MCP──▶ coding · github-mcp · jira · mcp-google · mcp-icloud
RECURRING: paperclip ──hermes_gateway──▶ hermes (same worker) + budget/audit
```

## Containers

| Service       | What it does | AI | Exposed |
|---------------|--------------|----|---------|
| `hermes`      | Worker + Telegram chat + MCP client | Claude Code (OAuth) | no (API :8642 internal) |
| `paperclip`   | Governance, recurring tasks, budget, audit | — | yes (:3100 dashboard) |
| `paperclip-db`| Paperclip's Postgres | — | no |
| `coding`      | MCP: Claude Code tools (`claude mcp serve`); hermes drives clone→edit→commit→push | Claude Code (OAuth) | no (:9100) |
| `github-mcp`  | MCP: official GitHub API (branch, commit, **PR**) | — | no (:9400) |
| `jira`        | MCP: Jira client (`mcp-atlassian`) | — | no (:9200) |
| `mcp-google`  | MCP: Google mail/calendar/drive (`workspace-mcp`) | — | no (:9000) |
| `mcp-icloud`  | MCP: iCloud SMTP mail sending | — | no (:9001) |

Custom (Go, tested): `mcp-icloud`. Community/official: Hermes, Paperclip, Claude Code
CLI (`coding`), GitHub MCP server (`github-mcp`), Google Workspace MCP
(`workspace-mcp`), Jira (`mcp-atlassian`). `coding`/`github-mcp` are official
CLIs/images bridged to HTTP with `supergateway` (no custom code).

## Setup

1. `cp .env.example .env` and fill in the secrets. (Hermes seeds its `config.yaml`
   into its volume from `hermes/config.example.yaml` on first boot — no copy needed.)
2. **Model providers**:
   - `hermes` → **OpenRouter**: key from https://openrouter.ai/keys → `OPENROUTER_API_KEY`.
     Model via **`HERMES_MODEL`** (applied at container boot; on Coolify change the env
     var + redeploy). Default is a free model with a free fallback; see `.env.example`.
     (Anthropic bans third-party apps from the subscription OAuth token, so Hermes can't
     use it.)
   - `coding` → **Claude Code subscription**: on your laptop run `claude setup-token`
     → paste into `CLAUDE_CODE_OAUTH_TOKEN`. This container runs the genuine Claude Code
     binary, which is allowed to use the subscription.
3. **Telegram**: create a bot with [@BotFather](https://t.me/BotFather),
   `TELEGRAM_BOT_TOKEN`. Get your id from [@userinfobot](https://t.me/userinfobot) →
   `TELEGRAM_ALLOWED_USERS` (and `TELEGRAM_HOME_CHANNEL`).
4. `API_SERVER_KEY` and `BETTER_AUTH_SECRET`: `openssl rand -base64 32` each.
5. `docker compose up -d --build`

## Connect Hermes to Paperclip (one-time, post-deploy)

The Telegram channel works out of the box. To have Hermes orchestrated **by**
Paperclip (recurring tasks, budget, audit):

1. Open the Paperclip dashboard (`http://localhost:3100`), create a company.
2. Add-agent → invite an agent with:
   - `adapterType: hermes_gateway`
   - `apiBaseUrl: http://hermes:8642`
   - `apiKey:` the same value as `API_SERVER_KEY`
3. Approve the join request. Paperclip claims a `PAPERCLIP_API_KEY` for Hermes.

Details: [Hermes Gateway Onboarding](https://github.com/paperclipai/paperclip/blob/master/doc/HERMES_GATEWAY_ONBOARDING.md).

## Local checks

```bash
# test the one kept Go module
(cd mcp-icloud && go test ./...)

# valid compose
docker compose config >/dev/null && echo OK

# stack up
docker compose up -d --build && docker compose ps
```

Flows (Telegram chat with the bot):
- "clone github.com/owner/repo, fix X, open a PR" → hermes uses `coding` (clone/edit/
  commit/push) + `github-mcp` (opens the PR).
- "send me a test email" → iCloud SMTP.
- "list my open Jira issues" → Jira client.

## Skills (share to coding + hermes)

Claude Code runs the CLI in the `coding` container (`claude mcp serve`) and inside
`paperclip` (the `claude_local` adapter, `/paperclip/.claude`). Hermes isn't Claude Code
but uses the same [agentskills.io](https://agentskills.io) `SKILL.md` standard and scans
`/opt/data/skills` + `/opt/data/plugin-skills` (`skills.external_dirs`). The `just sync-*`
recipes push to all three.

Push your local skills/plugins into the running stack with `just`:

```bash
just skills-list     # what would be pushed
just sync-skills     # ~/.claude/skills  -> coding + hermes   (local daemon)
just sync-plugins    # ~/.claude/plugins -> coding (native), flattened skills -> hermes
just sync-all        # both
```

`sync-*` need `docker compose up -d` first and target the **local** daemon.

**Remote (Coolify host over SSH):** rsyncs to the host, finds the containers by name
pattern + Coolify project/environment labels, then `docker cp` into them.

```bash
# host ssh alias via AGENTS_SSH_HOST (default: free-piva)
just sync-skills-remote  [project=agents] [environment=production]
just sync-plugins-remote [project=agents] [environment=production]
just sync-all-remote     [project=agents] [environment=production]
```

Sync **mirrors** local: a skill/plugin removed locally is removed in the container
too (the target dir is wiped, then repopulated). On hermes, personal skills go to
`/opt/data/skills` and flattened plugin skills to `/opt/data/plugin-skills` (separate
dirs so one mirror never wipes the other — both listed in `skills.external_dirs`).

## Coolify deploy

1. New project → **Docker Compose** resource pointed at this repo.
2. Set the secrets (same as `.env`) in the Coolify environment.
3. Give a domain to `paperclip` (→ 3100). Leave the other services **without** a
   domain (internal). Hermes needs no domain: it talks over Telegram and to Paperclip
   on the network.
4. Deploy.

## Notes

- `mcp-google` and `jira` use community packages via `uvx`: on first deploy verify
  package name/flags in `*/entrypoint.sh` (they can change).
- **Subscription OAuth token is banned for third-party apps** (Anthropic, since
  2026-02-20): Hermes cannot use `CLAUDE_CODE_OAUTH_TOKEN` — it 400s ("third-party apps
  draw from extra usage"). Only the genuine Claude Code binary (the `coding` container)
  may use it. Hermes therefore runs on OpenRouter (or any provider key). Don't try to
  spoof the Claude Code client — Anthropic verifies client identity and it risks a ban.
- The `paperclip` container builds from the official repo (remote git context): the
  first build is slow. A published image `ghcr.io/paperclipai/paperclip` also exists.
- `coding` requires `supergateway --stateful`: `claude mcp serve` keeps a session, so
  stateless mode (a process respawned per request) fails to initialize (`/mcp` →
  "not found").
