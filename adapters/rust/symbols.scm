; Rust symbols. More specific patterns first: the earliest matching pattern wins.

; Methods of a trait impl are qualified by the type and the trait: impl Display for Book
; { fn fmt() } is Book.Display.fmt. New inherent methods (Book.x) therefore never land in a
; trait impl.
(impl_item
  trait: [
    (type_identifier) @scope.1
    (generic_type type: (type_identifier) @scope.1)
    (scoped_type_identifier name: (type_identifier) @scope.1)
  ]
  type: [
    (type_identifier) @scope
    (generic_type type: (type_identifier) @scope)
    (scoped_type_identifier name: (type_identifier) @scope)
  ]
  body: (declaration_list
    (function_item name: (identifier) @name) @definition.method))

; Methods of an inherent impl are qualified by the type: impl Book { fn new() } is Book.new,
; also for generic types (impl<T> Wrapper<T>).
(impl_item
  type: [
    (type_identifier) @scope
    (generic_type type: (type_identifier) @scope)
    (scoped_type_identifier name: (type_identifier) @scope)
  ]
  body: (declaration_list
    (function_item name: (identifier) @name) @definition.method))

(impl_item
  type: [
    (type_identifier) @scope
    (generic_type type: (type_identifier) @scope)
  ]
  body: (declaration_list
    (const_item name: (identifier) @name) @definition.const))

; Trait methods (required and provided) are qualified by the trait automatically.
(trait_item
  body: (declaration_list
    [
      (function_item name: (identifier) @name)
      (function_signature_item name: (identifier) @name)
    ] @definition.method))

(function_item name: (identifier) @name) @definition.function
(struct_item name: (type_identifier) @name) @definition.type
(enum_item name: (type_identifier) @name) @definition.type
(union_item name: (type_identifier) @name) @definition.type
(type_item name: (type_identifier) @name) @definition.type
(trait_item name: (type_identifier) @name) @definition.interface
(const_item name: (identifier) @name) @definition.const
(static_item name: (identifier) @name) @definition.const
(macro_definition name: (identifier) @name) @definition.macro
; Inline modules qualify their items: mod tests { fn it_works() } is tests.it_works.
(mod_item name: (identifier) @name body: (declaration_list)) @definition.module
