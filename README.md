# foreman-code-ops

[Русская версия](README.ru.md)

An MCP server and Claude Code plugin that splits coding work between models:

- **Claude** plans the task and picks the symbols to change.
- **A local or cheap model** (the *generator*) writes the code.
- **The gate** (imports, formatter, linter, build) and an optional cloud **validator** check it.
- **The server** splices the code in with tree-sitter and commits every change.

Claude calls `upsert_symbol` instead of `Edit`/`Write`; each successful call is one commit with
`Foreman-Written-By`, `Foreman-Reviewed-By` and `Foreman-Attempts` trailers.

> **Status: early development (stage 7).** Go, Python, TypeScript/JavaScript and TSX/JSX work end
> to end: generation fitted to the slot, gate and fix loop, review and fallback by a cloud
> validator, commits with trailers, style guide, code graph integration, the Claude Code
> plugin, and links between the services of a monorepo. Rust and C# adapters, language servers
> as a fast gate stage, the Agent SDK provider and `stats` are experimental. See the
> [design plan](docs/design/plan-v4.ru.md) (Russian) for the roadmap.

## Supported

| | v1 |
|---|---|
| Languages | Go, Python, TypeScript/JavaScript, TSX/JSX; Rust and C# experimental; own adapters per project |
| Platforms | Linux; macOS best effort; Windows not in v1 |
| Generator | any OpenAI-compatible endpoint (llama-server, vLLM, LM Studio), OpenRouter, Anthropic, Claude Agent SDK |
| Validator | OpenRouter (default `z-ai/glm-5.3-flash`), OpenAI-compatible, Anthropic, Claude Agent SDK, or `none` |
| Code graph | codebase-memory-mcp, graphify, or none |

## Install

1. Install the server. Release binaries (Linux amd64/arm64; macOS best effort) are built by
   GitHub Actions:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/MarshMeg/foreman-code-ops-mcp/main/scripts/install.sh | sh
   ```

   The script picks the archive for your platform, checks it against `checksums.txt` and
   installs to `~/.local/bin` (`FOREMAN_BIN_DIR`, `FOREMAN_VERSION` change that). Or build from
   source (a C compiler is needed for the tree-sitter grammars):

   ```bash
   go install github.com/MarshMeg/foreman-code-ops-mcp/cmd/foreman-code-ops@latest
   ```

2. Install the Claude Code plugin (MCP server registration and the usage skill):

   ```
   /plugin marketplace add MarshMeg/foreman-code-ops-mcp
   /plugin install foreman-code-ops@foreman-code-ops
   ```

3. Prepare the project:

   ```bash
   foreman-code-ops init --project .
   ```

   This writes a draft `.foreman/workspace.yaml` (services found by `go.mod`, `package.json`,
   `tsconfig.json`, `pyproject.toml`, `Cargo.toml`, `*.csproj`), adds `permissions.deny` rules such as `Edit(**/*.go)` to
   `.claude/settings.json` so source edits go through foreman, and adds `.foreman/cache/` to
   `.gitignore`. Without the plugin, pass `--mcp` to register the server in `.mcp.json`.

4. Optionally install a code graph ([docs/graph.md](docs/graph.md)), e.g. codebase-memory-mcp.

With the plugin, the tools are named `mcp__plugin_foreman-code-ops_foreman__<tool>`; with a
project `.mcp.json` entry, `mcp__foreman__<tool>`.

## Languages

| Language | Files | Fix | Check | Tests |
|---|---|---|---|---|
| Go | .go | goimports | `go build` (module), `go vet` | `go test -run` |
| Python | .py .pyi | ruff (imports, format) | ruff, pyright or mypy | pytest `-k` |
| TypeScript | .ts .mts .cts | biome or prettier | `tsc` (project), biome lint or eslint | vitest or jest `-t` |
| TSX/JSX | .tsx .jsx | same | same | same |
| JavaScript | .js .mjs .cjs | same | `tsc` with `checkJs`, biome lint or eslint | same |
| Rust (experimental) | .rs | rustfmt | `cargo check` (crate) | `cargo test` |
| C# (experimental) | .cs | — | `dotnet build` | `dotnet test --filter` |

Tools are found in `node_modules/.bin`, `.venv/bin` or PATH and chosen by the project's config
files. Python has no compiler, so install pyright (or mypy) and keep the validator on.

With `lsp.enabled: true`, warm language servers (gopls, pyright, `tsc --lsp` or
typescript-language-server) check the changed files first and report most errors in well under
a second; the commands above still run before every commit. Projects can add or override
adapters in `.foreman/adapters/` ([docs/adapters.md](docs/adapters.md)).

## Configure

Settings are merged from, lowest to highest priority: built-in defaults,
`~/.config/foreman-code-ops/config.yaml`, `<project>/.foreman/config.yaml`, `FOREMAN_*`
environment variables, CLI flags. See [config.example.yaml](config.example.yaml) and
[docs/config.md](docs/config.md).

```bash
foreman-code-ops config --project .
```

prints the effective configuration.

```bash
foreman-code-ops stats --project . --since 7d
```

summarizes what foreman did: outcomes of `upsert_symbol` (written locally, by the validator,
first try), fix and review rounds, latency per tool and tokens and cost per model from the local
journal, plus the `Foreman-*` trailers of the git history (`--json` for machines).

Docs: [providers](docs/providers.md), [code graph](docs/graph.md), [adapters](docs/adapters.md), [links](docs/links.md).

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
| `discover_links` / `get_links` | map HTTP/gRPC calls between services ([docs/links.md](docs/links.md)) |
| `status` | providers, slots, queue, graph, warnings |

## License

[MIT](LICENSE)
