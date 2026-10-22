import { test } from "node:test";
import assert from "node:assert/strict";
import { buildOptions, handle, renderPrompt } from "./lib.mjs";

function fakeQuery(messages, { throwAfter } = {}) {
  const calls = [];
  const fn = ({ prompt, options }) => {
    calls.push({ prompt, options });
    return (async function* () {
      yield { type: "system", subtype: "init", tools: ["StructuredOutput"] };
      for (const m of messages) {
        yield m;
      }
      if (throwAfter) {
        throw new Error(throwAfter);
      }
    })();
  };
  fn.calls = calls;
  return fn;
}

const usage = { input_tokens: 10, output_tokens: 5, cache_read_input_tokens: 100, cache_creation_input_tokens: 20 };
const schema = { type: "object", properties: { code: { type: "string" } }, required: ["code"] };

test("renderPrompt quotes earlier turns", () => {
  assert.equal(renderPrompt([{ role: "user", content: "write f" }]), "write f");
  const p = renderPrompt([
    { role: "user", content: "write f" },
    { role: "assistant", content: "{bad" },
    { role: "user", content: "rejected: not JSON" },
  ]);
  assert.match(p, /<turn role="user">\nwrite f\n<\/turn>/);
  assert.match(p, /<turn role="assistant">\n\{bad\n<\/turn>/);
  assert.ok(p.endsWith("Current request:\n\nrejected: not JSON"));
  assert.throws(() => renderPrompt([]));
  assert.throws(() => renderPrompt([{ role: "assistant", content: "x" }]));
});

test("buildOptions disables tools and settings", () => {
  const o = buildOptions({ system: "sys", model: "claude-opus-5-5", schema, effort: "low" }, { cwd: "/tmp/x" });
  assert.deepEqual(o.tools, []);
  assert.deepEqual(o.settingSources, []);
  assert.equal(o.persistSession, false);
  assert.equal(o.systemPrompt, "sys");
  assert.equal(o.effort, "low");
  assert.deepEqual(o.outputFormat, { type: "json_schema", schema });
  const plain = buildOptions({}, {});
  assert.equal(plain.outputFormat, undefined);
  assert.equal(plain.model, undefined);
  assert.equal(plain.effort, undefined);
});

test("structured success returns the JSON and usage", async () => {
  const q = fakeQuery([{ type: "result", subtype: "success", is_error: false, result: "", structured_output: { code: "x" }, usage, total_cost_usd: 0.25 }]);
  const res = await handle({ id: 7, system: "s", messages: [{ role: "user", content: "go" }], schema }, q);
  assert.deepEqual(res, { id: 7, text: '{"code":"x"}', usage: { prompt_tokens: 130, completion_tokens: 5, cached_tokens: 100, cost: 0.25 } });
  assert.equal(q.calls[0].prompt, "go");
});

test("plain text answer", async () => {
  const q = fakeQuery([{ type: "result", subtype: "success", is_error: false, result: "hello", usage }]);
  const res = await handle({ id: 1, messages: [{ role: "user", content: "hi" }] }, q);
  assert.equal(res.text, "hello");
});

test("success without structured output is an error", async () => {
  const q = fakeQuery([{ type: "result", subtype: "success", is_error: false, result: "prose", usage }]);
  const res = await handle({ id: 2, messages: [{ role: "user", content: "go" }], schema }, q);
  assert.match(res.error, /without a structured answer/);
});

test("error result after which query throws", async () => {
  const q = fakeQuery([{ type: "result", subtype: "error_max_structured_output_retries", is_error: true, errors: ["schema mismatch"], usage }], { throwAfter: "query failed" });
  const res = await handle({ id: 3, messages: [{ role: "user", content: "go" }], schema }, q);
  assert.equal(res.error, "error_max_structured_output_retries: schema mismatch");
  assert.equal(res.usage.completion_tokens, 5);
});

test("query throws before any result", async () => {
  const q = fakeQuery([], { throwAfter: "Invalid API key" });
  const res = await handle({ id: 4, messages: [{ role: "user", content: "go" }] }, q);
  assert.deepEqual(res, { id: 4, error: "Invalid API key" });
});

test("bad request", async () => {
  const res = await handle({ id: 5, messages: [] }, fakeQuery([]));
  assert.match(res.error, /non-empty/);
});
