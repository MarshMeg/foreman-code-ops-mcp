## Python

- `code` is exactly one complete definition: a function, a method (with its decorators), or a
  class, with its docstring. No imports, no other definitions, no `if __name__ == "__main__"`.
- For a method, write it at column zero starting with `def` (or its decorators); it is
  indented into the class automatically. Keep `self` / `cls` as in the interface.
- List imports the code needs as full statements, e.g. `from app.domain import Book` or
  `import logging`. Unused imports are removed and imports are sorted automatically.
- Use type hints exactly as the interface shows. Raise specific exceptions; do not swallow
  them. Use only names that exist in the context you were given.
