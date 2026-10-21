; Go symbols. More specific patterns first: the first pattern matching a node wins.

(function_declaration
  name: (identifier) @name) @definition.function

; Methods are qualified by the receiver type: (s *BookService) List -> BookService.List
(method_declaration
  receiver: (parameter_list
    (parameter_declaration
      type: [
        (type_identifier) @scope
        (pointer_type (type_identifier) @scope)
        (generic_type type: (type_identifier) @scope)
        (pointer_type (generic_type type: (type_identifier) @scope))
      ]))
  name: (field_identifier) @name) @definition.method

(type_spec
  name: (type_identifier) @name
  type: (interface_type)) @definition.interface

(type_spec
  name: (type_identifier) @name) @definition.type

(type_alias
  name: (type_identifier) @name) @definition.type
