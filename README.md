# foreman-code-ops

[Русская версия](README.ru.md)

An MCP server and Claude Code plugin that splits coding work between models:

- **Claude** plans the task and picks the symbols to change.
- **A local or cheap model** (the *generator*) writes the code.
- **The gate** (imports, formatter, linter, build) and an optional cloud **validator** check it.
- **The server** splices the code in with tree-sitter and commits every change.

Claude calls `upsert_symbol` instead of `Edit`/`Write`; each successful call is one commit with
`Foreman-Written-By`, `Foreman-Reviewed-By` and `Foreman-Attempts` trailers.

> **Status: early development (stage 5).** Go is supported end to end: generation fitted to the
> slot, gate and fix loop, review and fallback by a cloud validator, commits with trailers, the
> project style guide, code graph integration and the Claude Code plugin. Python and TypeScript
> adapters are next. See the
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

1. Install the server (a C compiler is needed for the tree-sitter grammars):

   ```bash
   go install github.com/MarshMeg/foreman-code-ops/cmd/foreman-code-ops@latest
   ```

2. Install the Claude Code plugin (MCP server registration and the usage skill):

   ```
   /plugin install foreman-code-ops --marketplace MarshMeg/foreman-code-ops
   ```

3. Prepare the project:

   ```bash
   foreman-code-ops init --project .
   ```

   This writes a draft `.foreman/workspace.yaml` (services found by `go.mod`, `package.json`,
   `tsconfig.json`, `pyproject.toml`), adds `permissions.deny` rules such as `Edit(**/*.go)` to
   `.claude/settings.json` so source edits go through foreman, and adds `.foreman/cache/` to
   `.gitignore`. Without the plugin, pass `--mcp` to register the server in `.mcp.json`.

4. Optionally install a code graph ([docs/graph.md](docs/graph.md)), e.g. codebase-memory-mcp.

With the plugin, the tools are named `mcp__plugin_foreman-code-ops_foreman__<tool>`; with a
project `.mcp.json` entry, `mcp__foreman__<tool>`.

## Configure

Settings are merged from, lowest to highest priority: built-in defaults,
`~/.config/foreman-code-ops/config.yaml`, `<project>/.foreman/config.yaml`, `FOREMAN_*`
environment variables, CLI flags. See [config.example.yaml](config.example.yaml) and
[docs/config.md](docs/config.md).

```bash
foreman-code-ops config --project .
```

prints the effective configuration.

Docs: [providers](docs/providers.md), [code graph](docs/graph.md), [adapters](docs/adapters.md).

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
| `style_get` / `style_set` | read / update `.foreman/style.md` (whole file or one section) |
| `status` | providers, slots, queue, graph, warnings |

## License

[MIT](LICENSE)
