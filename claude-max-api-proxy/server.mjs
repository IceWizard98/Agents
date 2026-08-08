// OpenAI-compatible proxy that drives `claude --print` as a PLANNER (see lib.mjs).
// Hermes talks OpenAI /v1/chat/completions here; we translate its tools into a planner
// prompt, run the genuine Claude Code CLI on the Max subscription (the allowed path —
// no third-party HTTP with the OAuth token), and translate the model's proposed action
// back into an OpenAI tool_call. Hermes executes the tool and sends the result back.
import http from "node:http";
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { writeFileSync, unlinkSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  PLANNER_SYSTEM,
  buildSystemPrefix,
  buildConversation,
  parseModelOutput,
  toOpenAIResponse,
  planModels,
} from "./lib.mjs";

const PORT = Number(process.env.PORT || 3456);
const CLI_TIMEOUT_MS = Number(process.env.CLI_TIMEOUT_MS || 300000);
const MAX_CONCURRENCY = Number(process.env.MAX_CONCURRENCY || 4);
const MAX_BODY = Number(process.env.MAX_BODY || 10 * 1024 * 1024); // cap request body (OOM guard)
const MODELS = ["opus", "sonnet", "haiku"];

// The planner must PROPOSE, never execute — and the OAuth token lives in this process'
// env, which the Bash tool could read and exfiltrate. Hard-disable the CLI's built-in
// executing tools (the planner system prompt alone is not a trust boundary).
const DISALLOWED_TOOLS = [
  "Bash", "Edit", "Write", "Read", "Glob", "Grep", "WebFetch", "WebSearch",
  "NotebookEdit", "MultiEdit", "Task", "TodoWrite", "LS",
];

// Cap concurrent `claude` subprocesses (each is heavy) so a burst can't exhaust the box.
let active = 0;
const waiters = [];
function acquire() {
  if (active < MAX_CONCURRENCY) { active++; return Promise.resolve(); }
  return new Promise((r) => waiters.push(r)).then(() => { active++; });
}
function release() {
  active--;
  const next = waiters.shift();
  if (next) next();
}

// Map any incoming OpenAI model id to a bare CLI tier alias. Default is haiku (the cheap
// tier): an unknown/absent model takes the cheap-first path like everything else; a strong
// model is used only when explicitly named (planModels honors opus/sonnet without escalating).
function toAlias(model) {
  const m = String(model || "").replace(/^claude-code-cli\//, "");
  if (/opus/.test(m)) return "opus";
  if (/sonnet/.test(m)) return "sonnet";
  if (/haiku/.test(m)) return "haiku";
  return "haiku";
}

// Run `claude --print` once, return its stdout text. Rejects on non-zero exit.
// The STABLE prefix (planner protocol + action catalog + agent instructions) goes in the
// system prompt via --system-prompt-file; the VOLATILE conversation goes on stdin. This
// split is what makes Claude Code cache the catalog (1h TTL, measured) so repeated turns
// re-read it at ~10% cost instead of re-sending it. A file (not --system-prompt argv)
// avoids MAX_ARG_STRLEN / E2BIG on large catalogs.
function runClaude(systemPrompt, conversation, model, res) {
  return new Promise((resolve, reject) => {
    const sysFile = join(tmpdir(), `planner-sys-${randomUUID()}.txt`);
    // 0600: the file holds the planner system prompt; keep it unreadable to other users on
    // a shared host during the brief write..unlink window. (The OAuth token is NOT here.)
    try { writeFileSync(sysFile, systemPrompt, { mode: 0o600 }); } catch (e) { return reject(e); }
    let killTimer = null;
    // If the client hangs up before we've responded, kill the heavy child so it doesn't run
    // out CLI_TIMEOUT_MS pinning a concurrency slot (SIGTERM, then SIGKILL after a grace).
    const onClose = () => {
      if (res && res.writableEnded) return;
      child.kill("SIGTERM");
      killTimer = setTimeout(() => { try { child.kill("SIGKILL"); } catch { /* gone */ } }, 2000);
      killTimer.unref?.();
    };
    const cleanup = () => {
      try { unlinkSync(sysFile); } catch { /* already gone */ }
      if (res) res.removeListener("close", onClose);
      if (killTimer) clearTimeout(killTimer);
    };
    if (res) res.on("close", onClose);
    const args = [
      "--print",
      "--output-format", "json", // structured result → lets us read cache usage (see LOG_USAGE)
      "--exclude-dynamic-system-prompt-sections",
      "--system-prompt-file", sysFile,
      "--disallowedTools", ...DISALLOWED_TOOLS,
      "--model", model,
      "--no-session-persistence",
    ];
    const child = spawn("claude", args, { stdio: ["pipe", "pipe", "pipe"], env: process.env });
    let out = "", err = "";
    const timer = setTimeout(() => { child.kill("SIGTERM"); cleanup(); reject(new Error(`claude timed out after ${CLI_TIMEOUT_MS}ms`)); }, CLI_TIMEOUT_MS);
    child.stdin.on("error", () => {}); // ignore EPIPE if the CLI exits before reading all input
    child.stdin.write(conversation);
    child.stdin.end();
    child.stdout.on("data", (d) => { out += d; });
    child.stderr.on("data", (d) => { err += d; });
    child.on("error", (e) => { clearTimeout(timer); cleanup(); reject(e); });
    child.on("close", (code) => {
      clearTimeout(timer);
      cleanup();
      if (code !== 0) return reject(new Error(`claude exited ${code}: ${err.slice(0, 500)}`));
      let obj;
      try { obj = JSON.parse(out); } catch { return reject(new Error(`claude non-JSON output: ${out.slice(0, 300)}`)); }
      if (obj.is_error) return reject(new Error(`claude error: ${String(obj.result || obj.subtype || "unknown").slice(0, 300)}`));
      logUsage(obj.usage, model);
      resolve(String(obj.result || "").trim());
    });
  });
}

// One-line cache diagnostic, opt-in via LOG_USAGE=1. The whole point of the caching split
// is to turn re-sent catalog tokens into cache_read; if cache_creation dominates instead,
// the prefix isn't stable (varying tools/instructions) and we're paying the +25% write
// premium every turn. This surfaces which is happening on real Hermes traffic.
function logUsage(u, model) {
  if (!process.env.LOG_USAGE || !u) return;
  console.log(`[usage] model=${model} input=${u.input_tokens ?? 0} cache_read=${u.cache_read_input_tokens ?? 0} cache_write=${u.cache_creation_input_tokens ?? 0} output=${u.output_tokens ?? 0}`);
}

function sse(res, obj) { res.write(`data: ${JSON.stringify(obj)}\n\n`); }

function streamResponse(res, full, model) {
  const id = `chatcmpl-${randomUUID()}`;
  const created = Math.floor(Date.now() / 1000);
  const base = { id, object: "chat.completion.chunk", created, model };
  const chunk = (delta, finish = null) => sse(res, { ...base, choices: [{ index: 0, delta, finish_reason: finish }] });
  res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", Connection: "keep-alive" });
  chunk({ role: "assistant" });
  const msg = full.choices[0].message;
  if (msg.tool_calls) {
    chunk({ tool_calls: msg.tool_calls.map((tc, i) => ({ index: i, ...tc })) });
    chunk({}, "tool_calls");
  } else {
    if (msg.content) chunk({ content: msg.content });
    chunk({}, "stop");
  }
  res.write("data: [DONE]\n\n");
  res.end();
}

async function handleChat(req, res, body) {
  const hasTools = Array.isArray(body.tools) && body.tools.length > 0;
  const { first, escalate } = planModels(toAlias(body.model), hasTools);
  const messages = body.messages || [];
  const prefix = buildSystemPrefix(body.tools);
  const systemPrompt = prefix ? `${PLANNER_SYSTEM}\n\n${prefix}` : PLANNER_SYSTEM;
  const conversation = buildConversation(messages);
  let text, model = first, parsed;
  await acquire();
  try {
    text = await runClaude(systemPrompt, conversation, first, res);
    parsed = parseModelOutput(text);
    // Cheap-first: haiku only pays for chat turns. When it proposes a tool call, redo the
    // turn on sonnet (better at complex MCP arg schemas) and use that result instead.
    if (escalate && parsed.type === "action") {
      model = "sonnet";
      text = await runClaude(systemPrompt, conversation, model, res);
      parsed = parseModelOutput(text);
    }
    // Design blind spot made measurable (opt-in): a tool-bearing turn that returns prose is
    // usually a legit chat reply, but it's ALSO how a skipped-tool would look — the two are
    // indistinguishable, so we can't auto-correct it. Under LOG_USAGE, surface the rate so
    // it can be watched on real MCP traffic.
    if (process.env.LOG_USAGE && hasTools && parsed.type !== "action") {
      console.log(`[cheap-first] tool-bearing turn returned prose on model=${model} (no tool called)`);
    }
  } catch (e) {
    // Log the detail server-side; return a generic message. The CLI's stderr/result
    // is folded into e.message and could carry upstream credential-adjacent text —
    // don't echo it back to the caller (even internal).
    console.error(`[claude-max-api-proxy] cli error: ${String(e.message || e)}`);
    res.writeHead(502, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ error: { message: "upstream CLI error", type: "cli_error" } }));
    return;
  } finally {
    release();
  }
  const full = toOpenAIResponse(parsed, model, randomUUID());
  if (body.stream) streamResponse(res, full, model);
  else { res.writeHead(200, { "Content-Type": "application/json" }); res.end(JSON.stringify(full)); }
}

const server = http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/health") {
    res.writeHead(200, { "Content-Type": "application/json" });
    return res.end(JSON.stringify({ status: "ok", token_configured: !!process.env.CLAUDE_CODE_OAUTH_TOKEN }));
  }
  if (req.method === "GET" && req.url === "/v1/models") {
    res.writeHead(200, { "Content-Type": "application/json" });
    return res.end(JSON.stringify({ object: "list", data: MODELS.map((id) => ({ id, object: "model", owned_by: "anthropic" })) }));
  }
  if (req.method === "POST" && req.url === "/v1/chat/completions") {
    let data = "";
    let overflowed = false;
    req.on("data", (c) => {
      if (overflowed) return;
      data += c;
      if (data.length > MAX_BODY) {
        overflowed = true; // stop appending; guard the "end" handler too
        res.writeHead(413, { "Content-Type": "application/json", Connection: "close" });
        res.end(JSON.stringify({ error: { message: "request body too large", type: "payload_too_large" } }));
        req.destroy();
      }
    });
    req.on("end", () => {
      if (overflowed) return;
      let body;
      try { body = JSON.parse(data); } catch {
        res.writeHead(400, { "Content-Type": "application/json" });
        return res.end(JSON.stringify({ error: { message: "invalid JSON body", type: "bad_request" } }));
      }
      // JSON.parse("null"), a bare array, or a scalar are valid JSON but not a chat body;
      // handleChat derefs body.tools/.messages, so reject anything that isn't a plain object.
      if (typeof body !== "object" || body === null || Array.isArray(body)) {
        res.writeHead(400, { "Content-Type": "application/json" });
        return res.end(JSON.stringify({ error: { message: "invalid JSON body", type: "bad_request" } }));
      }
      // handleChat is async; an un-awaited rejection would crash the process (Node 22).
      handleChat(req, res, body).catch((e) => {
        if (!res.headersSent) {
          res.writeHead(500, { "Content-Type": "application/json" });
          res.end(JSON.stringify({ error: { message: "internal error", type: "internal" } }));
        } else { try { res.end(); } catch { /* already closed */ } }
      });
    });
    return;
  }
  res.writeHead(404, { "Content-Type": "application/json" });
  res.end(JSON.stringify({ error: { message: "not found", type: "not_found" } }));
});

// Best-effort sweep of planner-sys-* files a previous run left behind if it was killed
// hard (SIGKILL/crash skips the per-request cleanup). Runs once at boot before any request
// of this instance exists. Assumes one proxy instance per tmpdir (true per container); if
// two instances shared a tmpdir this could unlink a peer's in-flight file — not our deploy.
function sweepStaleTempFiles() {
  try {
    for (const f of readdirSync(tmpdir())) {
      if (f.startsWith("planner-sys-") && f.endsWith(".txt")) {
        try { unlinkSync(join(tmpdir(), f)); } catch { /* raced or gone */ }
      }
    }
  } catch { /* tmpdir unreadable — nothing to sweep */ }
}

server.listen(PORT, "0.0.0.0", () => {
  sweepStaleTempFiles();
  console.log(`[claude-max-api-proxy] planner proxy on 0.0.0.0:${PORT}`);
});
