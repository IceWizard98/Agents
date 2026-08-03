// Offline unit tests for the planner-protocol translation. Run: node test.mjs
import assert from "node:assert";
import {
  buildPlannerPrompt,
  parseModelOutput,
  toOpenAIResponse,
  resolveModel,
} from "./lib.mjs";

const tests = {
  builds_actions_and_conversation() {
    const p = buildPlannerPrompt(
      [{ role: "user", content: "weather in Rome?" }],
      [{ type: "function", function: { name: "get_weather", description: "w", parameters: { type: "object" } } }]
    );
    assert.ok(p.includes("ACTIONS the host can perform:"));
    assert.ok(p.includes('"get_weather"'));
    assert.ok(p.includes("USER: weather in Rome?"));
  },

  system_message_becomes_agent_instructions() {
    const p = buildPlannerPrompt(
      [{ role: "system", content: "be terse" }, { role: "user", content: "hi" }],
      []
    );
    assert.ok(p.includes("AGENT INSTRUCTIONS:"));
    assert.ok(p.includes("be terse"));
    assert.ok(!p.includes("USER: hi\nAGENT")); // instructions come before conversation
  },

  assistant_tool_call_and_tool_result_roundtrip() {
    const p = buildPlannerPrompt([
      { role: "user", content: "weather in Rome?" },
      { role: "assistant", content: null, tool_calls: [
        { id: "call_1", type: "function", function: { name: "get_weather", arguments: '{"city":"Rome"}' } }] },
      { role: "tool", tool_call_id: "call_1", content: '{"tempC":31}' },
    ], []);
    assert.ok(p.includes('ASSISTANT: {"action":{"name":"get_weather","arguments":{"city":"Rome"}}}'));
    assert.ok(p.includes("HOST (result of get_weather): {\"tempC\":31}"));
  },

  parse_action_block() {
    const r = parseModelOutput('```json\n{"action":{"name":"get_weather","arguments":{"city":"Rome"}}}\n```');
    assert.equal(r.type, "action");
    assert.equal(r.name, "get_weather");
    assert.deepEqual(r.arguments, { city: "Rome" });
    assert.equal(r.matched, true);
  },

  parse_final_block() {
    const r = parseModelOutput('```json\n{"final":"31C sunny"}\n```');
    assert.equal(r.type, "final");
    assert.equal(r.content, "31C sunny");
    assert.equal(r.matched, true);
  },

  parse_bare_json_action() {
    const r = parseModelOutput('{"action":{"name":"x","arguments":{}}}');
    assert.equal(r.type, "action");
    assert.equal(r.name, "x");
  },

  parse_plain_text_is_final_unmatched() {
    const r = parseModelOutput("Just a plain answer.");
    assert.equal(r.type, "final");
    assert.equal(r.content, "Just a plain answer.");
    assert.equal(r.matched, false); // caller uses this to detect a missed tool turn
  },

  builds_multiple_assistant_tool_calls() {
    const p = buildPlannerPrompt([
      { role: "assistant", content: null, tool_calls: [
        { id: "a", type: "function", function: { name: "t1", arguments: "{}" } },
        { id: "b", type: "function", function: { name: "t2", arguments: '{"x":1}' } },
      ] },
    ], []);
    assert.ok(p.includes('{"action":{"name":"t1","arguments":{}}}'));
    assert.ok(p.includes('{"action":{"name":"t2","arguments":{"x":1}}}'));
  },

  response_action_maps_to_tool_calls() {
    const out = toOpenAIResponse({ type: "action", name: "get_weather", arguments: { city: "Rome" } }, "sonnet", "abc");
    const tc = out.choices[0].message.tool_calls[0];
    assert.equal(out.choices[0].finish_reason, "tool_calls");
    assert.equal(tc.function.name, "get_weather");
    assert.deepEqual(JSON.parse(tc.function.arguments), { city: "Rome" });
    assert.equal(out.choices[0].message.content, null);
  },

  response_final_maps_to_content() {
    const out = toOpenAIResponse({ type: "final", content: "hi" }, "sonnet", "abc");
    assert.equal(out.choices[0].finish_reason, "stop");
    assert.equal(out.choices[0].message.content, "hi");
  },

  resolve_model_upgrades_haiku_when_tools() {
    assert.equal(resolveModel("haiku", true), "sonnet"); // haiku can't plan tools
    assert.equal(resolveModel("haiku", false), "haiku");  // plain chat stays haiku
    assert.equal(resolveModel("opus", true), "opus");
    assert.equal(resolveModel(undefined, false), "sonnet");
  },
};

let pass = 0;
for (const [name, fn] of Object.entries(tests)) {
  fn();
  console.log(`ok  ${name}`);
  pass++;
}
console.log(`\n${pass} passed`);
