# Changelog

All notable changes are listed here. Versions follow [semantic versioning](https://semver.org);
the release workflow takes the notes for a tag from its section.

## [Unreleased]

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
