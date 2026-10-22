# Language adapters

An adapter tells foreman how to find symbols in a language, what a new file looks like, and how
the gate fixes, checks and tests code. Adapters are plain YAML plus tree-sitter queries; a new
language needs no Go code as long as its tree-sitter grammar is compiled into the binary.

Built in ([`adapters/<lang>/`](../adapters), embedded into the binary):

| Adapter | Extensions | Gate |
|---|---|---|
| `go` | .go | goimports, go build, go vet, go test |
| `python` | .py, .pyi | ruff, pyright or mypy, pytest |
| `typescript` | .ts, .mts, .cts | biome/prettier/eslint, tsc, vitest/jest |
| `tsx` | .tsx, .jsx | same as typescript |
| `javascript` | .js, .mjs, .cjs | biome/prettier/eslint, tsc with checkJs, vitest/jest |
| `rust` (experimental) | .rs | rustfmt, cargo check, cargo test |
| `csharp` (experimental) | .cs | dotnet build, dotnet test |

Compiled-in grammars: `go`, `python`, `typescript`, `tsx`, `javascript`, `rust`, `c_sharp`.

The Rust and C# adapters are examples of adapters for languages with a different shape (methods
in `impl` blocks, types nested in namespaces). Their symbol handling is tested; their gate steps
are tested in CI only and have not been tried on real projects yet.

## Project adapters

A project can add or replace adapters without rebuilding foreman: put them under
`.foreman/adapters/<name>/` with the same layout as the built-in ones and commit them.

- An adapter whose `lang` matches a built-in one replaces it, e.g. to change the gate steps
  for one project.
- Any other `lang` adds a language. Its `grammar` must be one of the compiled-in grammars, so
  this works for dialects and DSLs that share a grammar (Starlark on `python`), or for a
  different take on a supported language.
- Errors (unknown keys, an invalid query, a grammar that is not compiled in) stop the server
  at startup with the file name; the log lists the project adapters that were loaded.

A grammar that is not compiled in needs a one-line change in
[`internal/lang/builtin/builtin.go`](../internal/lang/builtin/builtin.go) and a rebuild (the
grammar's Go binding must exist; the release builds use cgo for tree-sitter).

## Layout

```
adapters/go/
├── adapter.yaml
└── symbols.scm
```

## adapter.yaml

```yaml
lang: go                      # adapter name
grammar: go                   # tree-sitter grammar compiled into the binary
extensions: [.go]
symbols_query: symbols.scm    # query file next to adapter.yaml

# The signature of these kinds is the text before the named field (the body).
# Other kinds use their whole text as the signature (e.g. a struct type).
body_fields:
  function: body
  method: body

# Node kinds attached to a symbol when they sit directly above it (no blank line,
# not trailing other code on their line).
leading_comments: [comment]

# If a definition's parent has one of these kinds and holds only this definition,
# the parent becomes the symbol's range. Go: `type X struct{}` is a type_declaration
# around a single type_spec; in `type ( A int; B int )` each spec stays separate.
wrap_single: [type_declaration]

new_file:
  template: |                 # text/template; .Package is available
    package {{.Package}}
  # Package name taken from sibling files (first submatch); falls back to the directory name.
  package_regexp: '(?m)^package\s+([A-Za-z_][A-Za-z0-9_]*)'

prompt: prompt.md             # language guidance appended to the writer's system prompt
imports_query: imports.scm    # captures import declarations as @import
strip: [...]                  # regexes removed from generated code (package clause, imports)

# Where inserted imports go when the file has none: after these top-level nodes
# (expression_statement counts only as a lone string: a docstring or "use client").
import_preamble: [comment, expression_statement]
import_bare: "import {}"      # turns a bare module name into an import (Python)
nested_members: true          # methods live inside the class body (Python, TypeScript)
```

`body_fields` are looked up in the definition and its descendants, so the signature of
`export const f = async () => { ... }` stops before the arrow function's body. A new member
(`Class.method`) without `anchor` goes after the class's last member; with
`nested_members` and no members yet, the call is rejected before any model call.

Unknown keys are rejected.

## symbols.scm

Each pattern captures:

| Capture | Meaning |
|---|---|
| `@definition.<kind>` | the definition node; `<kind>` is what tools report (`function`, `method`, `type`, `interface`, `class`, `component`, …) |
| `@name` | the symbol's own name |
| `@scope` | optional qualifier, e.g. a Go method receiver type |
| `@scope.1`, `@scope.2`, … | further qualifiers, joined after `@scope` in order |

Symbol names are qualified automatically:

- by `@scope`: `func (s *BookService) List` → `BookService.List`;
- by several scopes: in Rust, `impl Display for Book { fn fmt }` captures the type as `@scope`
  and the trait as `@scope.1` → `Book.Display.fmt`;
- by enclosing definitions: a method inside a class → `Class.method`.

When several patterns match the same node, the **earliest pattern in the file wins**, so put
specific patterns (e.g. `type_spec` with an `interface_type`) before generic ones (any `type_spec`).

Example (Go):

```scheme
(method_declaration
  receiver: (parameter_list
    (parameter_declaration
      type: [
        (type_identifier) @scope
        (pointer_type (type_identifier) @scope)
      ]))
  name: (field_identifier) @name) @definition.method
```

Use `tree-sitter parse` or the [playground](https://tree-sitter.github.io/tree-sitter/7-playground.html)
to see node kinds and field names for your grammar.

## Testing an adapter

Add fixtures under `testdata/` and table tests next to the registry (see
`internal/lang/builtin/golang_test.go` and `rust_csharp_test.go`): symbol names and kinds,
ranges with doc comments and attributes, signatures, nested and grouped declarations, imports,
`Sanitize` and where a new member is inserted (`internal/pipeline/splice_test.go`). Gate tests
(`internal/gate/langs_test.go`) call `RequireTools` so they are skipped where the toolchain is
not installed.

## Toolchain (the gate)

The `toolchain` section tells the gate how to fix, check and test files. Candidate code lives
in memory until commit; checks see it through an overlay, so real files stay untouched.

```yaml
toolchain:
  root_markers: [go.mod]        # where commands run when no service covers the file
  overlay: true                 # checks read candidates via {overlay}
  package_pattern: ./{dir}      # package argument for a directory
  recursive_pattern: ./{dir}/...
  all_packages: ./...
  insert_imports: {name: insert imports, builtin: go-insert-imports}
  fix:                          # applied to every changed file, in order
    - {name: goimports, builtin: goimports}
  check:                        # the gate stops at the first failing step
    - name: go build
      run: [go, build, -overlay, "{overlay}", -o, /dev/null, "{packages}"]
      pattern: '^(?P<file>[^\s:#][^:]*\.go):(?P<line>\d+)(?::(?P<col>\d+))?: (?P<message>.*)$'
      scope: all                # whole root, so broken callers are caught
    - name: go vet
      run: [go, vet, -overlay, "{overlay}", "{packages}"]
      pattern: ...
  test:
    run: [go, test, -overlay, "{overlay}", "{packages}"]
    filter: [-run, "{filter}"]
```

**Steps** are either `builtin` (implemented in Go, see `internal/gate/builtin.go`: `goimports`,
`gofmt`, `go-insert-imports`) or `run` (an external command).

- A `fix` command receives the file on stdin and must print the new content on stdout
  (`{file}` is its path relative to the root).
- A `check` command fails the gate when it exits non-zero. Its output is parsed with `pattern`
  (named groups `file`, `line`, `col`, `message`); indented lines continue the previous
  diagnostic, unmatched output becomes one diagnostic without a file. `severity: warning`
  makes findings non-blocking. `scope: all` runs it on `all_packages` instead of the packages
  of the touched files.

**Placeholders:** `{file}`, `{files}`, `{packages}`, `{overlay}`, `{tmp}`, `{cache}` (stable per
root, for incremental builds), `{filter}`. An argument that is exactly `{files}` or `{packages}`
expands to several arguments.

### Choosing tools per project

```yaml
bin_dirs: [node_modules/.bin]          # searched before PATH, relative to the root
fix:
  - name: biome
    group: format                      # only the first applicable step of a group runs
    when: {files: [biome.json, biome.jsonc]}
    run: [biome, check, --write, "--stdin-file-path={file}"]
  - name: prettier
    group: format
    run: [prettier, --stdin-filepath, "{file}"]
check:
  - name: pyright
    group: typecheck                   # pyright if installed, else mypy
    run: [pyright, "{files}"]
    pattern: '^\s*(?P<file>/[^:]+\.pyi?):(?P<line>\d+):(?P<col>\d+) - (?P<severity>error|warning): (?P<message>.*)$'
    only_matches: true                 # drop progress and summary lines
tests:                                 # the first entry whose condition holds and command exists
  - name: vitest
    when: {deps: [vitest]}
    run: [vitest, run, --reporter=dot]
    filter: [-t, "{filter}"]
```

- `when`: `files` (any exists), `no_files` (none exists), `deps` (listed in package.json),
  `contains` (`{tsconfig.json: checkJs}`).
- `optional: true` skips a step whose command is not installed; grouped steps behave the same.
  Other missing commands fail the gate with "not found", and `status` lists them for the
  languages the project uses.
- `parser: eslint-json` reads `eslint --format json` instead of a regex.
- A `severity` group in `pattern` turns lines containing "warn" into warnings.

### Checking candidates without touching files

With `overlay: true` (Go) commands read candidates through `{overlay}`. Otherwise the gate syncs
a **shadow root**: a copy of the service root under the user cache directory where the changed
files hold the candidate. Unchanged files are copied once and then skipped by size and mtime;
heavy directories (`node_modules`, `.git`, virtualenvs, build output) are symlinked. Files are
copied rather than linked because Node and Python resolve symlinks to real paths (tests would
import the old code) and hard links would let a tool write through to the project. Paths in tool
output are mapped back to the project.

### Language servers

With `lsp.enabled: true` (experimental) the gate first opens the changed files, with their
candidate content, in a warm language server and collects its diagnostics. Errors found there
go straight back to the fix loop and the check commands are skipped; a clean result still runs
the commands, because only they see the whole root (a caller broken in an unchanged file). The
server reads every other file from disk; nothing is written.

```yaml
toolchain:
  lsp:                                   # the first entry whose condition holds and that starts
    - name: tsc
      when: {contains: {node_modules/typescript/package.json: '"version": "7.'}}
      run: [tsc, --lsp, --stdio]
      language_ids: {.ts: typescript, .tsx: typescriptreact}
    - name: typescript-language-server
      run: [typescript-language-server, --stdio]
      language_ids: {.ts: typescript, .tsx: typescriptreact}
      init_options: {}                   # sent as initializationOptions
      settle_ms: 500                     # push servers: wait for later updates (default 500)
```

Servers that support pull diagnostics (`textDocument/diagnostic`, e.g. TypeScript 7's
`tsc --lsp`) are asked directly; for the others foreman waits for `publishDiagnostics` of the
document version it sent, then `settle_ms` for later updates (servers often publish syntax
errors first and type errors later). One server runs per root and command, is shared by
adapters (ts and tsx), handles one check at a time, is restarted after a crash, and stops after
`lsp.idle_timeout`. A server that cannot start is skipped with a warning and retried after a
minute; the commands then run as usual.

Measured on the test fixtures (first request, then warm): pyright 1.4 s / 0.55 s, gopls
1.0 s / 0.5 s, typescript-language-server (TS 5.9) 1.6 s / 0.9 s, `tsc --lsp` (TS 7)
0.1 s / 2 ms. The warm push numbers are mostly `settle_ms`.

Built-in servers: `gopls`, `pyright-langserver`, `tsc --lsp` (TypeScript 7) or
`typescript-language-server`, `rust-analyzer` (not yet tried; it reports `cargo check`
results only for saved files, so only its own diagnostics apply).

## Walkthrough: the Rust adapter

The steps below are how [`adapters/rust`](../adapters/rust) was written; the same order works
for any language.

**1. Look at the tree.** Parse a representative file (doc comments, attributes, generics,
nested items) and print the top-level nodes, e.g. with `tree-sitter parse` or a throwaway test
that logs `node.ToSexp()`. For Rust this shows that `#[derive(...)]` is an `attribute_item`
*sibling* before the struct, that `///` doc comments are `line_comment` nodes that include
their newline, and that methods are `function_item`s inside `impl_item → declaration_list`.

**2. Decide what a symbol is and how it is named.** Claude addresses symbols as
`Type.method`, so the name must be unique within a file and predictable:

| Rust | Symbol |
|---|---|
| `pub struct Book` | `Book` (type) |
| `impl Book { fn new() }` | `Book.new` (method) |
| `impl Display for Book { fn fmt() }` | `Book.Display.fmt` — trait impls cannot take new methods, so they get their own prefix |
| `trait Repo { fn get(); }` | `Repo.get` |
| `mod tests { fn it_works() }` | `tests.it_works` |

`impl` blocks themselves are not symbols: two impls of one type would have the same name.

**3. Write `symbols.scm`, specific patterns first.** The trait-impl pattern comes before the
inherent-impl pattern, which comes before the plain `function_item` pattern, because the
earliest matching pattern wins:

```scheme
(impl_item
  trait: (type_identifier) @scope.1
  type: (type_identifier) @scope
  body: (declaration_list (function_item name: (identifier) @name) @definition.method))

(impl_item
  type: (type_identifier) @scope
  body: (declaration_list (function_item name: (identifier) @name) @definition.method))

(function_item name: (identifier) @name) @definition.function
```

The real file also accepts generic (`Wrapper<T>`) and path (`fmt::Display`) types.

**4. Ranges and signatures.** `leading_comments: [line_comment, block_comment, attribute_item]`
attaches doc comments and attributes, so replacing `Book` keeps its `#[derive]`.
`body_fields` (`function: body`, `method: body`, …) make the signature stop before the body.

**5. Members.** `nested_members: true`: a new `Book.title_len` goes after the last `Book.*`
method, i.e. into the inherent impl (trait-impl methods are `Book.Display.*` and do not count).
A type with no methods yet gets them when Claude rewrites the type as struct + `impl` block;
`extraSymbols` accepts `Book.*` symbols in a rewrite of `Book`. Tell the model in `prompt.md`.

**6. Imports and generated code.** `imports.scm` captures `use_declaration`; `strip` removes
`use` lines a model writes into `code` anyway (top-level only: `^use`, so `use` inside a function
body stays); the generic `insert-imports` builtin adds the answer's `imports` after the last `use`
or after the crate doc comment and inner attributes (`import_preamble`).

**7. Toolchain.** `root_markers: [Cargo.toml]`; no overlay, so candidates are checked in a
shadow root. `cargo check --message-format=short` prints `file:line:col: error[E…]: message`,
parsed with a regex whose `severity` group keeps warnings non-blocking. `--target-dir
{cache}/target` keeps builds incremental and out of the project. `scope: all` checks the whole
crate so broken callers fail the gate. `rustfmt` reads stdin and is `optional`.

**8. Prompt.** `prompt.md` states the answer's shape (one item, no `use`, methods without the
`impl` wrapper) and the conventions the gate cannot check (no `unwrap()` outside tests).

**9. Tests.** Symbols, ranges with attributes, signatures, imports, `Sanitize`, the insertion
point of a new method, and a gate test that runs where `cargo` exists.

## C# notes

[`adapters/csharp`](../adapters/csharp) is closer to TypeScript: members live in type bodies and
are qualified by their enclosing types (`BookService.Cache.Clear`). Namespaces do not qualify
names, so file-scoped (`namespace X;`) and block namespaces behave the same. Attributes are
children of the declaration in this grammar, so they are part of the symbol without
`leading_comments`. A new file takes the namespace of a sibling file (`package_regexp`).
`root_markers: ["*.csproj"]` shows that markers may be globs. There is no stdin formatter in the
default toolchain; add one (for example CSharpier) as a `fix` step in a project adapter.
