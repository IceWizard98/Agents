// OpenAI-compatible proxy that drives `claude --print` as a PLANNER (see lib.mjs).
// Hermes talks OpenAI /v1/chat/completions here; we translate its tools into a planner
// prompt, run the genuine Claude Code CLI on the Max subscription (the allowed path —
// no third-party HTTP with the OAuth token), and translate the model's proposed action
// back into an OpenAI tool_call. Hermes executes the tool and sends the result back.
import http from "node:http";
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import {
  PLANNER_SYSTEM,
  buildPlannerPrompt,
  parseModelOutput,
  toOpenAIResponse,
  resolveModel,
} from "./lib.mjs";

const PORT = Number(process.env.PORT || 3456);
const CLI_TIMEOUT_MS = Number(process.env.CLI_TIMEOUT_MS || 300000);
const MAX_CONCURRENCY = Number(process.env.MAX_CONCURRENCY || 4);
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

// Map any incoming OpenAI model id to a bare CLI tier alias.
function toAlias(model) {
  const m = String(model || "").replace(/^claude-code-cli\//, "");
  if (/opus/.test(m)) return "opus";
  if (/sonnet/.test(m)) return "sonnet";
  if (/haiku/.test(m)) return "haiku";
  return "sonnet";
}

// Run `claude --print` once, return its stdout text. Rejects on non-zero exit.
function runClaude(prompt, model) {
  return new Promise((resolve, reject) => {
    const args = [
      "--print",
      "--exclude-dynamic-system-prompt-sections",
      "--system-prompt", PLANNER_SYSTEM,
      "--disallowedTools", ...DISALLOWED_TOOLS,
      "--model", model,
      "--no-session-persistence",
    ];
    // Prompt goes on STDIN, not argv: a real agent conversation (system + skills + MCP
    // tool schemas + history) easily exceeds Linux MAX_ARG_STRLEN (128 KB per argv
    // element), which would fail spawn with E2BIG. --print reads the prompt from stdin.
    const child = spawn("claude", args, { stdio: ["pipe", "pipe", "pipe"], env: process.env });
    let out = "", err = "";
    const timer = setTimeout(() => { child.kill("SIGTERM"); reject(new Error(`claude timed out after ${CLI_TIMEOUT_MS}ms`)); }, CLI_TIMEOUT_MS);
    child.stdin.on("error", () => {}); // ignore EPIPE if the CLI exits before reading all input
    child.stdin.write(prompt);
    child.stdin.end();
    child.stdout.on("data", (d) => { out += d; });
    child.stderr.on("data", (d) => { err += d; });
    child.on("error", (e) => { clearTimeout(timer); reject(e); });
    child.on("close", (code) => {
      clearTimeout(timer);
      if (code === 0) resolve(out.trim());
      else reject(new Error(`claude exited ${code}: ${err.slice(0, 500)}`));
    });
  });
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
  const model = resolveModel(toAlias(body.model), hasTools);
  const prompt = buildPlannerPrompt(body.messages || [], body.tools);
  let text;
  await acquire();
  try {
    text = await runClaude(prompt, model);
  } catch (e) {
    res.writeHead(502, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ error: { message: String(e.message || e), type: "cli_error" } }));
    return;
  } finally {
    release();
  }
  const parsed = parseModelOutput(text);
  // A tool turn that came back as prose (not a protocol block) means Hermes gets a
  // guessed answer instead of a tool call — surface it instead of failing silently.
  if (hasTools && !parsed.matched) {
    console.warn(`[claude-max-api-proxy] tool turn produced no action block (model=${model}); returning prose as final`);
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
    req.on("data", (c) => { data += c; });
    req.on("end", () => {
      let body;
      try { body = JSON.parse(data); } catch {
        res.writeHead(400, { "Content-Type": "application/json" });
        return res.end(JSON.stringify({ error: { message: "invalid JSON body", type: "bad_request" } }));
      }
      handleChat(req, res, body);
    });
    return;
  }
  res.writeHead(404, { "Content-Type": "application/json" });
  res.end(JSON.stringify({ error: { message: "not found", type: "not_found" } }));
});

server.listen(PORT, "0.0.0.0", () => {
  console.log(`[claude-max-api-proxy] planner proxy on 0.0.0.0:${PORT}`);
});
