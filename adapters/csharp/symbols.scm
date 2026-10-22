; C# symbols. Members are qualified by their enclosing types automatically
; (BookService.Get, BookService.Cache.Clear). Namespaces do not qualify names.
; More specific patterns first: the earliest matching pattern wins.

(method_declaration name: (identifier) @name) @definition.method
(constructor_declaration name: (identifier) @name) @definition.method
(destructor_declaration name: (identifier) @name) @definition.method
(property_declaration name: (identifier) @name) @definition.property
(event_declaration name: (identifier) @name) @definition.property

(class_declaration name: (identifier) @name) @definition.class
(record_declaration name: (identifier) @name) @definition.class
(struct_declaration name: (identifier) @name) @definition.type
(interface_declaration name: (identifier) @name) @definition.interface
(enum_declaration name: (identifier) @name) @definition.type
(delegate_declaration name: (identifier) @name) @definition.type
