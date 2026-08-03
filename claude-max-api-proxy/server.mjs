// Launcher for the claude-max-api-proxy npm package.
//
// Why not just run its own `claude-max-api` bin: that standalone entrypoint calls
// startServer({ port }) with no host, so Express binds 127.0.0.1 — unreachable from
// other containers on agents_net. We import startServer directly and bind 0.0.0.0.
//
// We also skip the package's verifyClaude/verifyAuth pre-checks: this runs headless
// with CLAUDE_CODE_OAUTH_TOKEN in env (same pattern as the `coding` container), and
// verifyAuth's interactive "run claude auth login" hint is meaningless here.
import { startServer } from "claude-max-api-proxy/dist/server/index.js";

const port = Number(process.env.PORT || 3456);
const host = process.env.HOST || "0.0.0.0";

await startServer({ port, host });
console.log(`[launcher] claude-max-api-proxy listening on ${host}:${port}`);
