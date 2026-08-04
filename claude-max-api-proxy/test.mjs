// Offline unit tests for the planner-protocol translation. Run: node test.mjs
import assert from "node:assert";
import {
  buildPlannerPrompt,
  buildSystemPrefix,
  buildConversation,
  parseModelOutput,
  toOpenAIResponse,
  planModels,
  slimSchema,
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

  system_prefix_holds_catalog_only_not_agent_instructions() {
    const tools = [{ type: "function", function: { name: "get_weather", description: "w", parameters: { type: "object" } } }];
    const prefix = buildSystemPrefix(tools);
    assert.ok(prefix.includes("ACTIONS the host can perform:"));
    assert.ok(prefix.includes('"get_weather"'));
    // agent instructions + conversation must NOT be in the cached prefix (they can vary per turn)
    assert.ok(!prefix.includes("AGENT INSTRUCTIONS:"));
    assert.ok(!prefix.includes("Conversation:"));
  },

  system_prefix_sorts_actions_for_stable_cache() {
    const mk = (n) => ({ type: "function", function: { name: n, description: "", parameters: {} } });
    // same set, different order -> identical prefix, or the cache would never hit
    assert.equal(buildSystemPrefix([mk("b"), mk("a")]), buildSystemPrefix([mk("a"), mk("b")]));
    assert.ok(buildSystemPrefix([mk("b"), mk("a")]).indexOf('"a"') < buildSystemPrefix([mk("b"), mk("a")]).indexOf('"b"'));
  },

  system_prefix_empty_without_tools() {
    assert.equal(buildSystemPrefix([]), "");
    assert.equal(buildSystemPrefix(undefined), "");
  },

  conversation_holds_agent_instructions_and_convo() {
    const conv = buildConversation([{ role: "system", content: "be terse" }, { role: "user", content: "ciao" }]);
    assert.ok(conv.includes("AGENT INSTRUCTIONS:")); // agent instructions moved here (volatile)
    assert.ok(conv.includes("be terse"));
    assert.ok(conv.includes("Conversation:"));
    assert.ok(conv.includes("USER: ciao"));
    assert.ok(!conv.includes("ACTIONS")); // catalog must NOT ride on the volatile stdin
  },

  split_recomposes_to_planner_prompt() {
    const msgs = [{ role: "system", content: "sys" }, { role: "user", content: "hi" }];
    const tools = [{ type: "function", function: { name: "t", description: "d", parameters: {} } }];
    const whole = buildPlannerPrompt(msgs, tools);
    assert.equal(whole, `${buildSystemPrefix(tools)}\n\n${buildConversation(msgs)}`);
  },

  slim_strips_nested_prose_keeps_structure() {
    const s = slimSchema({
      type: "object",
      description: "outer prose",
      properties: {
        city: { type: "string", description: "the city", examples: ["Rome"] },
        unit: { type: "string", enum: ["c", "f"], default: "c" },
      },
      required: ["city"],
    });
    // prose keywords gone
    assert.equal(s.description, undefined);
    assert.equal(s.properties.city.description, undefined);
    assert.equal(s.properties.city.examples, undefined);
    assert.equal(s.properties.unit.default, undefined);
    // structure the model needs kept
    assert.equal(s.properties.city.type, "string");
    assert.deepEqual(s.properties.unit.enum, ["c", "f"]);
    assert.deepEqual(s.required, ["city"]);
  },

  slim_keeps_property_literally_named_description() {
    // a tool arg named "description" must survive — it's a property key, not a keyword
    const s = slimSchema({
      type: "object",
      properties: { description: { type: "string", description: "the note body" } },
      required: ["description"],
    });
    assert.ok(s.properties.description, "property named description must be kept");
    assert.equal(s.properties.description.type, "string");
    assert.equal(s.properties.description.description, undefined); // its own prose stripped
    assert.deepEqual(s.required, ["description"]);
  },

  slim_recurses_items_and_combinators() {
    const s = slimSchema({
      type: "array",
      items: { type: "object", description: "d", properties: { x: { type: "number", title: "X" } } },
      anyOf: [{ type: "string", description: "gone" }],
    });
    assert.equal(s.items.description, undefined);
    assert.equal(s.items.properties.x.title, undefined);
    assert.equal(s.items.properties.x.type, "number");
    assert.equal(s.anyOf[0].description, undefined);
    assert.equal(s.anyOf[0].type, "string");
  },

  build_prompt_slims_tool_schemas() {
    const p = buildPlannerPrompt(
      [{ role: "user", content: "hi" }],
      [{ type: "function", function: {
        name: "send_email", description: "send an email",
        parameters: { type: "object", properties: { body: { type: "string", description: "VERBOSE_BODY_PROSE" } } },
      } }]
    );
    assert.ok(p.includes('"send_email"'));
    assert.ok(p.includes("send an email")); // top-level tool description kept (planner picks by it)
    assert.ok(!p.includes("VERBOSE_BODY_PROSE")); // nested schema prose stripped
  },

  plan_models_haiku_escalates_only_on_tool_turns() {
    // chat turn (no tools): haiku only, never escalate
    assert.deepEqual(planModels("haiku", false), { first: "haiku", escalate: false });
    // tool turn: run haiku first, escalate to sonnet if it proposes an action
    assert.deepEqual(planModels("haiku", true), { first: "haiku", escalate: true });
    // explicit strong model: honor it, single run, no escalation
    assert.deepEqual(planModels("sonnet", true), { first: "sonnet", escalate: false });
    assert.deepEqual(planModels("opus", true), { first: "opus", escalate: false });
    // default (missing alias) is the cheap path
    assert.deepEqual(planModels(undefined, false), { first: "haiku", escalate: false });
  },
};

let pass = 0;
for (const [name, fn] of Object.entries(tests)) {
  fn();
  console.log(`ok  ${name}`);
  pass++;
}
console.log(`\n${pass} passed`);
