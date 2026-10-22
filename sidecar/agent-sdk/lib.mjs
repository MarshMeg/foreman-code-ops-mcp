// Request handling for the foreman-code-ops Agent SDK sidecar. Kept apart from the stdin/stdout
// loop in index.mjs so tests can pass a fake query function.

/**
 * Renders the chat transcript as one prompt. The Agent SDK takes a single prompt per query, so
 * earlier turns (a rejected answer and the feedback on it) are quoted before the last request.
 * @param {{role: string, content: string}[]} messages
 */
export function renderPrompt(messages) {
  if (!Array.isArray(messages) || messages.length === 0) {
    throw new Error("messages must be a non-empty array");
  }
  const last = messages[messages.length - 1];
  if (last.role !== "user") {
    throw new Error("the last message must come from the user");
  }
  if (messages.length === 1) {
    return last.content;
  }
  const earlier = messages
    .slice(0, -1)
    .map((m) => `<turn role="${m.role === "assistant" ? "assistant" : "user"}">\n${m.content}\n</turn>`)
    .join("\n\n");
  return `Earlier turns of this conversation:\n\n${earlier}\n\nCurrent request:\n\n${last.content}`;
}

/**
 * Builds query() options for one structured answer with no tools and no user or project
 * settings, so the answer depends only on the request.
 */
export function buildOptions(req, { cwd, env, abortController }) {
  const options = {
    systemPrompt: req.system || undefined,
    model: req.model || undefined,
    // StructuredOutput is the only tool; it is added by outputFormat.
    tools: [],
    settingSources: [],
    strictMcpConfig: true,
    persistSession: false,
    permissionMode: "dontAsk",
    // The structured answer is a tool round trip; leave room for a schema retry.
    maxTurns: 4,
    cwd,
    env,
    abortController,
  };
  if (req.effort) {
    options.effort = req.effort;
  }
  if (req.schema) {
    options.outputFormat = { type: "json_schema", schema: req.schema };
  }
  return options;
}

/**
 * Runs one request and returns the response object written back to foreman.
 * @param {object} req {id, system, messages, schema?, model?, effort?}
 * @param {Function} query the Agent SDK query function
 */
export async function handle(req, query, { cwd, env, signal } = {}) {
  const abortController = new AbortController();
  signal?.addEventListener("abort", () => abortController.abort(), { once: true });
  let result;
  try {
    const prompt = renderPrompt(req.messages);
    for await (const message of query({ prompt, options: buildOptions(req, { cwd, env, abortController }) })) {
      if (message.type === "result") {
        result = message;
      }
    }
  } catch (err) {
    // A single-shot query throws after yielding an error result; report the result's errors.
    if (!result || result.subtype === "success") {
      return { id: req.id, error: String(err?.message ?? err) };
    }
  }
  if (!result) {
    return { id: req.id, error: "the Agent SDK returned no result" };
  }
  const usage = toUsage(result);
  if (result.subtype !== "success" || result.is_error) {
    const why = result.errors?.length ? result.errors.join("; ") : result.result || "no details";
    return { id: req.id, error: `${result.subtype}: ${why}`, usage };
  }
  let text = result.result ?? "";
  if (req.schema) {
    if (result.structured_output === undefined || result.structured_output === null) {
      return { id: req.id, error: "the agent finished without a structured answer", usage };
    }
    text = JSON.stringify(result.structured_output);
  }
  return { id: req.id, text, usage };
}

function toUsage(result) {
  const u = result.usage ?? {};
  const cacheRead = u.cache_read_input_tokens ?? 0;
  const cacheWrite = u.cache_creation_input_tokens ?? 0;
  return {
    prompt_tokens: (u.input_tokens ?? 0) + cacheRead + cacheWrite,
    completion_tokens: u.output_tokens ?? 0,
    cached_tokens: cacheRead,
    cost: result.total_cost_usd ?? 0,
  };
}
