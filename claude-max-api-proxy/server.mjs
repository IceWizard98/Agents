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
const MODELS = ["opus", "sonnet", "haiku"];

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
      "--model", model,
      "--no-session-persistence",
      prompt,
    ];
    // ponytail: built-in tool execution isn't hard-disabled (the --allowedTools flag is
    // variadic and eats the prompt); the planner system prompt keeps the model proposing
    // rather than executing. Enumerate --disallowedTools here if a model ever executes.
    const child = spawn("claude", args, { stdio: ["pipe", "pipe", "pipe"], env: process.env });
    let out = "", err = "";
    const timer = setTimeout(() => { child.kill("SIGTERM"); reject(new Error(`claude timed out after ${CLI_TIMEOUT_MS}ms`)); }, CLI_TIMEOUT_MS);
    child.stdin.end(); // no stdin: prompt is an arg
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
  try {
    text = await runClaude(prompt, model);
  } catch (e) {
    res.writeHead(502, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ error: { message: String(e.message || e), type: "cli_error" } }));
    return;
  }
  const parsed = parseModelOutput(text);
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
