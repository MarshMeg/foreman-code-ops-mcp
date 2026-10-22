# Changelog

All notable changes are listed here. Versions follow [semantic versioning](https://semver.org);
the release workflow takes the notes for a tag from its section.

## [0.1.2]

Installation is now global once and per project after that.

- The plugin no longer starts the MCP server in every project. `foreman-code-ops init` registers
  it in the project's `.mcp.json` and approves it in `.claude/settings.json`
  (`enabledMcpjsonServers`); `--mcp=false` skips that.
- Skills: `/foreman-code-ops:setup` installs the binary and graphify and writes the models to the
  global config; `/foreman-code-ops:connect` connects a project and continues in a new session.
- graphify is the default code graph. New tools `graph_overview`, `graph_query`, `graph_node`,
  `graph_path`, `graph_affected` give Claude the codebase structure through foreman; the graph
  is built by `init`, refreshed after foreman's commits and rebuilt when older than the last
  commit. graphify always runs without LLM backends.
- `init` writes a foreman section into `CLAUDE.md` (read code through the graph, write it through
  foreman; `--no-claude-md` skips it), ignores `graphify-out/` and builds the graph (`--no-graph`).
- `delete_symbol` also runs a name search when the graph finds no references: graphs miss some
  uses (e.g. calls through struct fields).
- Fixed: graphify reindex failed on every commit (`graphify update` has no `--no-viz`).
- Fixed: an answer truncated at `max_tokens` failed the call instead of falling back to the
  validator.

## [0.1.1]

- Plugin skill `/foreman-code-ops:setup`: installs the binary and configures a project (init,
  workspace review, generator and validator, tool checks, style guide, commit).

## [0.1.0]

First public version.

- MCP server with `upsert_symbol`, `get_symbol`, `delete_symbol`, `create_file`, `delete_file`,
  `check`, `run_tests`, `style_get`, `style_set`, `discover_links`, `get_links`, `status`.
- Generator: OpenAI-compatible endpoints (llama-server with slots, `/props` and `/tokenize`),
  OpenRouter, Anthropic, Claude Agent SDK (Node sidecar). Prompts fitted to the slot context.
- Gate: imports, formatters, build/lint checks and tests per language, on candidates that never
  touch the working tree (Go overlay, shadow roots elsewhere); optional language servers as a
  fast first stage.
- Validator review (none/minor/major), fix rounds, fallback to the validator, `mode=direct`.
- Commits of only the touched files with `Foreman-Written-By`, `Foreman-Reviewed-By` and
  `Foreman-Attempts` trailers; `foreman-code-ops stats`.
- Languages: Go, Python, TypeScript, TSX/JSX, JavaScript; Rust and C# experimental; project
  adapters in `.foreman/adapters/`.
- Code graph: codebase-memory-mcp, graphify, custom command; grep fallback.
- Monorepo links between services (HTTP routes and calls, OpenAPI, protobuf, docker-compose).
- Claude Code plugin and `foreman-code-ops init`.
