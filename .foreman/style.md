# Style guide

Applies to all code in this repository.

- Everything a model or MCP client sees (tool descriptions, prompts, schema descriptions, error
  texts) is in English.
- Comments explain why, not what: full sentences, wrapped at about 100 columns. Exported or
  public items get a doc comment that starts with their name.
- Keep functions small and focused; prefer early returns over nested branches.
- No new dependencies without a strong reason; prefer the standard library.
- Never write to stdout from the server: stdout is the MCP transport.

## Go

- Format with gofmt and goimports; the code must pass `golangci-lint run` (standard linters plus
  errorlint, misspell, unconvert, gocritic).
- Wrap errors with context: `fmt.Errorf("doing x %q: %w", name, err)`; lower-case messages with
  no trailing period, often prefixed with the subsystem (`agent-sdk: ...`). Use `errors.New` for
  constant messages and `errors.Is` / `errors.As` to inspect errors.
- Log through the injected `*slog.Logger` (stderr); default a nil logger to
  `slog.New(slog.DiscardHandler)`.
- Pass `context.Context` as the first argument to anything that does I/O or may block; apply
  timeouts with `context.WithTimeout` and `defer cancel()`.
- Constructors are `NewX(opts XOptions) (*X, error)`; options structs hold configuration, the
  type holds state. Guard shared state with a `sync.Mutex` named `mu` placed above the fields it
  protects.
- Short receiver and local names (`a`, `o`, `req`, `cfg`); JSON struct tags in snake_case with
  `omitempty` for optional fields.
- Tests use only the standard `testing` package (no testify): `t.Fatalf` / `t.Errorf` with
  `got X, want Y` messages, `t.TempDir()` for files, helpers from `internal/testutil`. Tests that
  need external tools skip when they are missing.

## JavaScript

- ES modules (`.mjs`, `import` / `export`), Node 18+, no build step and no TypeScript.
- Two-space indent, double quotes, semicolons, trailing commas in multi-line literals.
- Exported functions get a JSDoc block; add `@param` types for non-obvious arguments.
- Keep I/O (stdin/stdout loops, process handling) in `index.mjs` and pure logic in `lib.mjs` so
  it can be tested with injected fakes.
- Throw `Error` with a plain lower-case message; return errors to the Go side as an `error`
  string in the response object rather than crashing the process.
- Tests use `node:test` and `node:assert/strict`, one `test("does x", ...)` per behaviour, run
  with `pnpm test` (`node --test`).
