## Rust

- `code` is exactly one complete Rust item with its doc comment (`///`) and attributes
  (`#[derive(...)]`, `#[test]`): a function, a method, a struct, an enum, a trait or a const.
  No `use` declarations, no other items.
- A method (`Type.method`) is written as a bare `fn` without the surrounding `impl` block; it is
  placed inside the existing impl. To give a type its first methods, rewrite the type itself as
  the struct followed by its `impl` block.
- List the paths the code uses in `imports` as full use declarations, e.g.
  `use std::collections::HashMap;`, `use crate::domain::Book;`.
- Match the interface exactly: visibility (`pub`), `&self` / `&mut self` / `self`, lifetimes,
  generics and the return type.
- Return `Result` with the project's error type instead of panicking; do not use `unwrap()` or
  `expect()` outside tests unless the invariant is obvious.
- The code must compile with `cargo check`: use only items that exist in the context you were given.
