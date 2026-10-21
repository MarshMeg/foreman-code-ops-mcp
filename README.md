# foreman-code-ops

[Русская версия](README.ru.md)

An MCP server and Claude Code plugin that splits coding work between models:

- **Claude** plans the task and picks the symbols to change.
- **A local or cheap model** (the *generator*) writes the code.
- **The gate** (imports, formatter, linter, build) and an optional cloud **validator** check it.
- **The server** splices the code in with tree-sitter and commits every change.

Claude calls `upsert_symbol` instead of `Edit`/`Write`; each successful call is one commit with
`Foreman-Written-By`, `Foreman-Reviewed-By` and `Foreman-Attempts` trailers.

> **Status: early development (stage 4).** For Go, the full pipeline works: generation fitted to
> the slot, gate and fix loop, review by a cloud validator (OpenRouter, Anthropic or any
> OpenAI-compatible API), fallback to the validator, `mode=direct`, commit with trailers. Style
> guide tools, the code graph and the plugin are next. See [docs/providers.md](docs/providers.md). See the
> [design plan](docs/design/plan-v4.ru.md) (Russian) for the roadmap.

## Supported

| | v1 |
|---|---|
| Languages | Go, Python, TypeScript/JavaScript, TSX/JSX |
| Platforms | Linux; macOS best effort; Windows not in v1 |
| Generator | any OpenAI-compatible endpoint (llama-server, vLLM, LM Studio), OpenRouter, Anthropic |
| Validator | OpenRouter (default `z-ai/glm-5.3-flash`), OpenAI-compatible, Anthropic, or `none` |
| Code graph | codebase-memory-mcp, graphify, or none |

## Install

```bash
go install github.com/MarshMeg/foreman-code-ops/cmd/foreman-code-ops@latest
```

Register it in your project's `.mcp.json`:

```json
{
  "mcpServers": {
    "foreman": {
      "command": "foreman-code-ops",
      "args": ["--project", "${CLAUDE_PROJECT_DIR}"]
    }
  }
}
```

Optionally describe the services of a monorepo:

```bash
foreman-code-ops init --project .
```

writes a draft `.foreman/workspace.yaml` from `go.mod`, `package.json`, `tsconfig.json` and
`pyproject.toml` markers.

## Configure

Settings are merged from, lowest to highest priority: built-in defaults,
`~/.config/foreman-code-ops/config.yaml`, `<project>/.foreman/config.yaml`, `FOREMAN_*`
environment variables, CLI flags. See [config.example.yaml](config.example.yaml) and
[docs/config.md](docs/config.md).

```bash
foreman-code-ops config --project .
```

prints the effective configuration.

A local generator with three 64k slots:

```bash
llama-server -m model.gguf -c 196608 -np 3 --port 8080
```

## Tools

| Tool | Purpose |
|---|---|
| `upsert_symbol` | create or replace a function, method, type, class, interface or component |
| `get_symbol` | source and signature of a symbol |
| `delete_symbol` | delete a symbol (checks references unless `force`) |
| `create_file` / `delete_file` | explicit file operations |
| `check` | run the gate without changes |
| `run_tests` | run tests on request |
| `style_get` / `style_set` | read / update `.foreman/style.md` |
| `status` | providers, slots, queue, graph, warnings |

## License

[MIT](LICENSE)
