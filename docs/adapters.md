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

## Coming next

The gate part of an adapter (imports, formatting, lint/build, tests) is added in stage 2.
