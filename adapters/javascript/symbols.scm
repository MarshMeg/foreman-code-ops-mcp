; JavaScript symbols. More specific patterns first: the earliest matching pattern wins.

; Components: capitalized functions (React convention). Hooks (useX) stay functions.
((function_declaration
  name: (identifier) @name) @definition.component
  (#match? @name "^[A-Z]"))

((lexical_declaration
  (variable_declarator
    name: (identifier) @name
    value: [(arrow_function) (function_expression)])) @definition.component
  (#match? @name "^[A-Z]"))

(method_definition
  name: [(property_identifier) (private_property_identifier)] @name) @definition.method

(function_declaration
  name: (identifier) @name) @definition.function

(generator_function_declaration
  name: (identifier) @name) @definition.function

(class_declaration
  name: (identifier) @name) @definition.class

(lexical_declaration
  (variable_declarator
    name: (identifier) @name
    value: [(arrow_function) (function_expression)])) @definition.function
