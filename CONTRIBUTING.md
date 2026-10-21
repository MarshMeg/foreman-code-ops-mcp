# Contributing

Thanks for your interest in foreman-code-ops.

## Development

Requirements: Go (version in `go.mod`), git, a C compiler (tree-sitter grammars are built with cgo).
[golangci-lint](https://golangci-lint.run) v2 for linting.

Python and TypeScript adapter tests need ruff, pyright, mypy, pytest, typescript, prettier,
biome, eslint and vitest; they are skipped when missing. Install them globally, or into a prefix
and point `FOREMAN_TEST_PATH` (a PATH-style list) at their bin directories:

```bash
FOREMAN_TEST_PATH=$HOME/tools/py/bin:$HOME/tools/node/node_modules/.bin go test -race ./...
golangci-lint run
go run ./cmd/foreman-code-ops config --project .
```

The server speaks MCP over stdio, so **never write to stdout** outside the MCP transport; log via
the `*slog.Logger` (stderr).

## Layout

| Path | What |
|---|---|
| `cmd/foreman-code-ops` | entry point, subcommands |
| `internal/config` | config schema, defaults, layered loading |
| `internal/mcp` | MCP tools and their input/output schemas |
| `internal/ops` | operations behind the tools: resolve, edit, commit |
| `internal/lang` | adapter interface, registry; `generic/` YAML-driven adapter; `builtin/` grammars |
| `internal/edit` | byte-range splicing with re-indentation |
| `internal/workspace` | staged changes, diff, atomic apply with rollback |
| `internal/gitops` | dirty checks, commits of touched files only, name search |
| `internal/project` | project root, `.foreman/workspace.yaml`, safe path resolution |
| `internal/journal` | JSONL log of operations |
| `adapters/` | built-in language adapters (YAML + queries) |
| `testdata/` | fixture projects |
| `docs/design` | design plan |

## Conventions

- Everything the models or MCP clients see — tool descriptions, prompts, schema descriptions — is
  in English.
- A new setting goes into `Config`, `Default()`, the `settings` table in `internal/config/load.go`
  (which gives it an env var and a flag), and `config.example.yaml`. A test fails if the example
  drifts from the defaults.
- User-facing docs are kept in English and Russian (`README.md` / `README.ru.md`).
- Commits: short imperative subject, e.g. `config: add git.branch`.

## Language adapters

Adapters are described in YAML plus tree-sitter queries (`adapters/<lang>/`); see
[docs/adapters.md](docs/adapters.md).

## Releases

Pushing a `v*` tag builds each platform natively on its own runner (cgo rules out simple
cross-compilation) and publishes a GitHub release with archives and `checksums.txt`.
