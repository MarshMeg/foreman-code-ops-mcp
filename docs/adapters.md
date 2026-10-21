# Language adapters

An adapter tells foreman how to find symbols in a language and what a new file looks like.
Built-in adapters live in [`adapters/<lang>/`](../adapters) and are embedded into the binary.
They are plain YAML plus tree-sitter queries, so a new language needs no Go code except
registering its grammar in `internal/lang/builtin`.

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
```

Unknown keys are rejected.

## symbols.scm

Each pattern captures:

| Capture | Meaning |
|---|---|
| `@definition.<kind>` | the definition node; `<kind>` is what tools report (`function`, `method`, `type`, `interface`, `class`, `component`, …) |
| `@name` | the symbol's own name |
| `@scope` | optional qualifier, e.g. a Go method receiver type |

Symbol names are qualified automatically:

- by `@scope`: `func (s *BookService) List` → `BookService.List`;
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
`internal/lang/builtin/golang_test.go`): symbol names and kinds, ranges with doc comments,
signatures, nested and grouped declarations.

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

**Placeholders:** `{file}`, `{files}`, `{packages}`, `{overlay}`, `{tmp}`, `{filter}`. An argument
that is exactly `{files}` or `{packages}` expands to several arguments.

Languages without overlay support (`overlay: false`) can check files on disk (`check` tool),
but not unapplied candidates yet; that comes with the Python and TypeScript adapters.
