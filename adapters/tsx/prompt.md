## TypeScript / TSX

- `code` is exactly one complete declaration: a function, an arrow function constant, a class,
  a method, an interface or a type, or a component (a capitalized function returning JSX), with its JSDoc/TSDoc comment. Keep `export` when the
  symbol is exported. No imports, no other declarations.
- For a method, write only the method (`name(args): Type { ... }`) at column zero; it is
  indented into the class automatically.
- List imports as full statements, e.g. `import { BookService } from "../service/book";` or
  `import type { Book } from "../domain/book";`. A bare module name is not enough.
- Match the interface's types exactly; avoid `any`. Hooks are functions named `useX`. Handle errors explicitly; do not swallow promise
  rejections. Use only names that exist in the context you were given.
