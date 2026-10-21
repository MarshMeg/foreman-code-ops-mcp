## Go

- `code` is exactly one complete Go declaration: a function, a method, or a type, with its doc
  comment. No `package` clause, no `import` block, no other declarations.
- Doc comments start with the symbol name: `// List returns ...`.
- For a method, use the receiver name and pointer/value receiver shown in the interface or the
  existing code.
- List the import paths the code uses in `imports`, e.g. `"context"`, `"fmt"`,
  `"example.com/app/internal/domain"`, or with a name: `apperr "example.com/app/internal/errors"`.
  Standard library and existing imports may be omitted; goimports fixes them.
- Return errors instead of panicking; wrap them with `fmt.Errorf("...: %w", err)` unless the
  project style says otherwise. Accept `context.Context` as the first parameter when the
  interface has it.
- The code must compile: use only identifiers that exist in the context you were given.
