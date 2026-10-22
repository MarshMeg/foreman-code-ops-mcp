---
name: foreman-code-ops
description: How to read and change source code in projects connected to foreman-code-ops — explore the codebase through foreman's code graph (graph_overview, graph_query, graph_node, graph_path, graph_affected) and write and delete functions, methods, types, classes and components through the foreman MCP tools (upsert_symbol, delete_symbol, create_file, check, run_tests, style_set) instead of editing files directly. Use whenever you would read, search or edit Go, Python, TypeScript, JavaScript, Rust or C# source in a project with a .foreman directory or the foreman MCP server (tools mcp__foreman__*). If the project is not connected yet, use the connect skill.
---

# Changing code with foreman-code-ops

In this project, source files are changed through the foreman MCP server, not with Edit or
Write. You plan; a cheaper model writes each symbol; the server fixes imports, formats, builds,
lints, optionally has a validator review it, and commits every successful change.

## Language

Write everything you send to foreman in **English**: `spec`, `interface`, `commit_message`,
`style_set` content. Keep talking to the user in their language.

## Before you change code

1. Call `status` once per session. Note warnings: a missing generator, an unavailable
   validator (then nothing is reviewed), a missing code graph, tools missing from PATH.
2. Call `style_get`. If there is no style guide, offer the user to create one (see below).
3. Understand the code first, through the graph (next section).

## Reading the code: the graph

foreman keeps a code graph of the project (graphify): symbols, files and their edges (calls,
references, imports, inheritance), grouped into communities. It is refreshed after every
foreman commit and rebuilt when it is older than the last commit. Prefer it over reading or
grepping whole files:

- `graph_overview` — the project's structure: core abstractions (most connected nodes),
  communities, surprising connections, import cycles. Call it once when you start on a project.
- `graph_query {question}` — the symbols relevant to a question, with `file:Lline` and edges.
  Narrow with `seeds` (start nodes) or `context` (e.g. `call`); raise `budget` when the answer
  says it was truncated.
- `graph_node {name}` — one symbol or file and its connections in both directions. When a name
  is ambiguous, the answer lists candidates; retry with `path::symbol` or the node id.
- `graph_path {from, to}` — how one symbol reaches another.
- `graph_affected {name}` — what depends on a symbol: check it before changing a signature or
  deleting it.
- `get_symbol {file, symbol}` — the exact current source of one symbol, once you know where it is.

The graph is built from syntax, so it can miss some uses (for example calls through struct
fields or interfaces); read the code when the answer matters. Uncommitted changes are not in
the graph until the next commit.

## Writing a symbol: `upsert_symbol`

One call writes one symbol: a function, method (`Type.Method`), type, interface, class or
component. If it exists it is replaced, otherwise inserted: after `anchor`; a new method
after the last member of its class; anything else at the end of the file (created if needed,
with `package` for new Go files). In Python and TypeScript a method can only be added to a
class that already has a member — otherwise pass `anchor`, or upsert the whole class.

- **spec**: what it must do, concretely: inputs, outputs, errors, edge cases. The writer is
  a small model; leave nothing important implicit.
- **interface**: the exact signature (and struct fields / props when relevant).
- **context**: references `{file, symbol}` to everything the code uses: types, interfaces,
  functions it calls, the DTOs it returns. Pass references, never pasted source. Missing
  context is the most common cause of failures.
- **Order by layers** — data → service → transport — and wait for each result: use the
  returned `signature` when you describe the next symbol. Independent symbols can be written
  in parallel.
- **Keep every step buildable.** The whole module is built after each change. Add an
  implementation before the interface method it satisfies, and new functions before their
  callers. To change a signature that has callers, add the new version under a new name,
  move the callers to it, then `delete_symbol` the old one.
- **mode**: leave `local` for routine code (CRUD, mapping, wiring, simple components). Use
  `direct` for complex logic or algorithms: the validator writes it right away.
- **run_tests**: pass `true` (the package's tests) or a test name filter when the change has
  tests worth running. Results are reported, never auto-fixed.
- **limits**: raise `max_build_fixes` / `max_reviews` for a known-hard task.

### Reading the result

- `status: applied` — committed. Check `written_by` (`local` or `validator`) and `reviewed_by`.
  If `reviewed_by` is empty (no validator, or the review was skipped), **read the `diff`
  yourself** before building on it. `minor_notes` are optional polish.
- `status: failed` — nothing was written. Read `diagnostics` and `last_code`, then either
  improve the spec, interface or context and retry, or retry with `mode: direct`.
- `status: refused` — the file has uncommitted user changes. Tell the user; do not work around it.
- `warnings` mention dropped context (the generator slot was too small) or an unavailable
  validator.

## Monorepos: links between services

If `.foreman/workspace.yaml` declares several services, run `discover_links` once and commit
it; foreman keeps `.foreman/links.json` current afterwards. Before changing an HTTP handler, a
DTO or a client call, call `get_links` for it, and change both sides: pass the other side as
`context` (a frontend hook gets the backend handler and its response type). `delete_symbol`
refuses to remove a handler that another service calls.

## Other tools

- `get_symbol {file, symbol}` — source, signature and lines of one symbol.
- `delete_symbol {file, symbol, force?}` — refuses while references exist (code graph; a name
  search, which may over-report, when the graph is unavailable or finds none) or when the build
  breaks. Update callers first; use
  `force` only when you are sure.
- `create_file` / `delete_file` — explicit file operations, also committed.
- `check {paths?}` — build and lint without changing anything.
- `run_tests {target?, filter?}` — run tests on demand.

## Files foreman does not handle

Edit and Write are denied for source files foreman manages. Non-source files (go.mod,
package.json, pyproject.toml, `.mjs`/`.cjs` configs, docs, SQL, YAML) are edited normally. If a change cannot be expressed as symbols
(for example, a package-level `var` block), tell the user instead of bypassing the rules.

## Project style guide

`.foreman/style.md` is shown to the writer and the reviewer. If it is missing, offer to create
it: study the codebase, then call `style_set` with sections such as:

- `## Errors` — creation, wrapping, sentinel errors
- `## Logging`
- `## Constructors and dependency injection`
- `## Layer naming` (repository, service, handler)
- `## Context and cancellation`
- `## Comments`
- one section per language: `## Go`, `## Python`, `## TypeScript`, `## JavaScript`

Keep it short and concrete; update one section with `style_set {section, content}`.
