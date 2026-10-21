# Model providers

foreman-code-ops uses two models: the **generator** writes code, the **validator** (next stage)
reviews it. They are configured separately (see [config.md](config.md)).

## Generator

Available now: `provider: openai`, any OpenAI-compatible chat completions endpoint
(llama-server, vLLM, LM Studio, OpenAI). `openrouter`, `anthropic` and `agent-sdk` follow.

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

If a server rejects `response_format` with HTTP 400, foreman switches to `grammar` for the rest
of the session. Every answer is validated against the schema on the server side; an invalid
answer gets one retry with the validation error.

### The fix loop

The generated code is spliced into the file, imports are inserted and fixed, the file is
formatted, and the gate runs (`go build` for the whole module, `go vet` for the package). On
errors the generator gets the diagnostics and its previous answer, up to
`limits.max_build_fixes` times. The answer must define exactly the requested symbol; extra
helpers are rejected and sent back as an error.

Generation does not block other operations. If the file changes while the model is working,
the code is re-applied to the new version and checked again before commit.
