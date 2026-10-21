## JavaScript

- `code` is exactly one complete declaration: a function, an arrow function constant, a class,
  a method, or a component (a capitalized function returning JSX), with its JSDoc comment.
  Keep `export` when the symbol is exported. No imports, no other declarations.
- For a method, write only the method (`name(args) { ... }`) at column zero; it is indented
  into the class automatically.
- List imports as full statements, e.g. `import { BookService } from "../service/book.js";`.
  A bare module name is not enough.
- Follow the interface described in the task exactly. Handle errors explicitly; do not swallow
  promise rejections. Use only names that exist in the context you were given.
