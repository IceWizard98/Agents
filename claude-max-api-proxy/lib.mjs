// Pure translation between OpenAI chat/completions and the "planner protocol" we
// drive over `claude --print`. No I/O here — unit-tested in test.mjs.
//
// Why a planner protocol instead of native tool-calling: the Claude Code CLI is an
// AGENT, not a bare LLM, and it does not accept arbitrary OpenAI/Anthropic tool
// definitions except via MCP servers it would then EXECUTE itself. Hermes needs the
// opposite: the model must PROPOSE a tool call and let Hermes run it. So we frame
// Hermes' tools as "actions", instruct the model to emit a JSON action (never execute),
// and translate that back into an OpenAI tool_call. Verified live: opus/sonnet comply,
// haiku does not (too weak for the meta-protocol) — see SYSTEM below and the callers.

export const PLANNER_SYSTEM = `You are a PLANNER. You output the next action as JSON for a host program to carry out. You do not and cannot perform actions; you only decide the next one. The host performs the action and sends you the result, then you decide again.

Respond with EXACTLY ONE of:
1) An action request — a fenced \`\`\`json block: {"action":{"name":"<name>","arguments":{...}}}
2) A final answer — a fenced \`\`\`json block: {"final":"<your answer text>"}

Never write anything outside the single json block. The listed actions always work when you request them; the host runs them. Do not talk about availability, permissions, tools, or external websites.`;

// haiku cannot follow the planner protocol reliably; upgrade tool-bearing turns.
const TOOL_CAPABLE = new Set(["opus", "sonnet"]);
export function resolveModel(alias, hasTools) {
  const a = alias || "sonnet";
  if (hasTools && !TOOL_CAPABLE.has(a)) return "sonnet";
  return a;
}

function contentToText(content) {
  if (content == null) return "";
  if (typeof content === "string") return content;
  return content
    .filter((p) => p && (p.type === "text" || typeof p.text === "string"))
    .map((p) => p.text || "")
    .join("");
}

function toolsToActions(tools) {
  return (tools || []).map((t) => {
    const fn = t.function || t;
    return { name: fn.name, description: fn.description || "", arguments_schema: fn.parameters || {} };
  });
}

// Build the single prompt string handed to `claude --print`. The planner protocol
// lives in PLANNER_SYSTEM (passed via --system-prompt); this is the conversation body.
export function buildPlannerPrompt(messages, tools) {
  const lines = [];
  const actions = toolsToActions(tools);
  if (actions.length) {
    lines.push("ACTIONS the host can perform:");
    lines.push(JSON.stringify(actions));
    lines.push("");
  }

  // Map tool_call_id -> action name so a later tool result can name its action.
  const idToName = {};
  for (const m of messages) {
    for (const tc of m.tool_calls || []) idToName[tc.id] = tc.function?.name || "action";
  }

  const agentInstructions = messages
    .filter((m) => m.role === "system")
    .map((m) => contentToText(m.content))
    .filter(Boolean)
    .join("\n\n");
  if (agentInstructions) {
    lines.push("AGENT INSTRUCTIONS:");
    lines.push(agentInstructions);
    lines.push("");
  }

  lines.push("Conversation:");
  for (const m of messages) {
    if (m.role === "system") continue;
    if (m.role === "user") {
      lines.push(`USER: ${contentToText(m.content)}`);
    } else if (m.role === "assistant") {
      if (m.tool_calls && m.tool_calls.length) {
        const tc = m.tool_calls[0];
        let args = {};
        try { args = JSON.parse(tc.function?.arguments || "{}"); } catch { args = {}; }
        lines.push(`ASSISTANT: ${JSON.stringify({ action: { name: tc.function?.name, arguments: args } })}`);
      } else {
        lines.push(`ASSISTANT: ${contentToText(m.content)}`);
      }
    } else if (m.role === "tool") {
      const name = idToName[m.tool_call_id] || "action";
      lines.push(`HOST (result of ${name}): ${contentToText(m.content)}`);
    }
  }
  return lines.join("\n");
}

// Extract the JSON object from the model's reply (fenced block or bare) and classify.
export function parseModelOutput(text) {
  const raw = String(text || "").trim();
  let jsonStr = null;
  const fence = raw.match(/```(?:json)?\s*([\s\S]*?)```/i);
  if (fence) {
    jsonStr = fence[1].trim();
  } else if (raw.startsWith("{")) {
    jsonStr = raw;
  }
  if (jsonStr) {
    try {
      const obj = JSON.parse(jsonStr);
      if (obj && obj.action && obj.action.name) {
        return { type: "action", name: obj.action.name, arguments: obj.action.arguments || {} };
      }
      if (obj && typeof obj.final === "string") {
        return { type: "final", content: obj.final };
      }
    } catch { /* fall through to plain text */ }
  }
  // No recognizable protocol block -> treat the whole reply as the final answer.
  return { type: "final", content: raw };
}

export function toOpenAIResponse(parsed, model, id) {
  const base = {
    id,
    object: "chat.completion",
    created: Math.floor(Date.now() / 1000),
    model,
  };
  if (parsed.type === "action") {
    return {
      ...base,
      choices: [{
        index: 0,
        message: {
          role: "assistant",
          content: null,
          tool_calls: [{
            id: `call_${id}`,
            type: "function",
            function: { name: parsed.name, arguments: JSON.stringify(parsed.arguments) },
          }],
        },
        finish_reason: "tool_calls",
      }],
    };
  }
  return {
    ...base,
    choices: [{ index: 0, message: { role: "assistant", content: parsed.content }, finish_reason: "stop" }],
  };
}
