# Model providers

foreman-code-ops uses two models: the **generator** writes code, the **validator** reviews it
and takes over when the generator fails. They are configured separately (see [config.md](config.md)).

| Provider | Generator | Validator | Notes |
|---|---|---|---|
| `openai` | yes | yes | any OpenAI-compatible endpoint (llama-server, vLLM, LM Studio, OpenAI) |
| `openrouter` | yes | yes (default) | key from `OPENROUTER_API_KEY`; reports cost and cached tokens |
| `anthropic` | yes | yes | official Go SDK; key from `ANTHROPIC_API_KEY` or an `ant auth login` profile |
| `agent-sdk` | later | later | stage 7 |
| `none` | — | yes | disables review and fallback; the gate is the only check |

## Generator

```yaml
generator:
  provider: openai
  base_url: http://127.0.0.1:8080/v1
  model: qwen2.5-coder-7b
  slots: 3
  slot_ctx: 65536
  output_reserve: 8192
  structured: json_schema
```

### Slots

llama-server splits its context (`-c`) evenly between parallel slots (`-np`):

```bash
llama-server -m model.gguf -c 196608 -np 3 --port 8080
```

Set `generator.slots` and `generator.slot_ctx` to match. foreman runs at most `slots`
generations at once and fits every prompt into `slot_ctx - output_reserve` tokens.

When the endpoint is a llama-server, foreman reads `/props` on the first `upsert_symbol` or
`status` call. If the server reports fewer slots or a smaller slot context than configured,
`status` shows a warning and the smaller value is used. Tokens are then counted exactly with
`/tokenize`; for other servers they are estimated.

### Fitting the prompt

When the prompt does not fit, parts are dropped in this order: the file's imports, the
signatures of other declarations in the file, the project style guide, then context symbols
(the last one first). The task, interface and current code are never dropped; if they alone do
not fit, `upsert_symbol` fails with `context_too_large`. Dropped parts are listed in the result's
warnings.

### Structured output

The generator answers `{"code": "...", "imports": [...]}`.

| `structured` | What is sent |
|---|---|
| `json_schema` | `response_format: {type: json_schema, strict: true}` |
| `grammar` | llama.cpp's `json_schema` field; the server compiles it to a GBNF grammar |
| `none` | instructions only |

If a server rejects `response_format` with HTTP 400, foreman switches to `grammar` (OpenRouter:
`none`) for the rest of the session. The Anthropic provider uses structured outputs
(`output_config.format`). Every answer is validated against the schema on the server side; an invalid
answer gets one retry with the validation error.

### The fix loop

The generated code is spliced into the file, imports are inserted and fixed, the file is
formatted, and the gate runs (`go build` for the whole module, `go vet` for the package). On
errors the generator gets the diagnostics and its previous answer, up to
`limits.max_build_fixes` times. The answer must define exactly the requested symbol; extra
helpers are rejected and sent back as an error.

Generation does not block other operations. If the file changes while the model is working,
the code is re-applied to the new version and checked again before commit.

## Validator

```yaml
validator:
  provider: openrouter
  model: z-ai/glm-5.3-flash
  timeout: 120s
```

After the code passes the gate, the validator reviews it against the spec, interface, context
and style guide and answers `{ok, severity, issues, rewrite}`:

- `none` / `minor`: accepted; minor issues are returned as `minor_notes` and never trigger
  another round.
- `major`: the generator gets the issues and fixes the code (up to `limits.max_reviews` rounds,
  each through the gate again).

**Fallback.** When the generator runs out of build-fix or review rounds, or cannot produce
valid JSON, the validator writes the symbol itself: first from the `rewrite` of its last
review if it gave one, else from a fresh prompt with the full, unbudgeted context and the
generator's last attempt. Its code goes through the gate up to `limits.max_fallback_attempts`
times. The commit then says `Foreman-Written-By: validator (...)`.

**`mode=direct`** skips the generator: the validator writes the symbol right away. Claude uses
it for complex logic and algorithms.

If the reviewer is unreachable, a result that passed the gate is still committed, without
`Foreman-Reviewed-By`, and the response carries a "review skipped" warning. If the validator is
misconfigured (for example, a missing key), every response and `status` say so.

At most `limits.max_parallel_reviews` validator requests run at once. Reviews put the style
guide and context first so provider prompt caches can reuse them. With Anthropic models, the
system prompt is cached explicitly, and Claude Fable 5.1 / Opus 5.5 / Opus 5 get server-side
refusal fallbacks (`fallbacks: "default"`).

## Measuring validators

```bash
OPENROUTER_API_KEY=... foreman-code-ops bench-review \
  --models z-ai/glm-5.3-flash,z-ai/glm-5.3-flashx --parallel 1,4,8 --n 16
```

sends a fixed, realistic review request and prints p50/p95 latency, the share of prompt tokens
served from cache and the cost of 100 reviews (OpenRouter reports cost per request).

### Results

Not measured yet. Paste the table here after running the command above.
