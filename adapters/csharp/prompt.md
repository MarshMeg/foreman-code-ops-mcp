## C#

- `code` is exactly one complete member or type with its XML doc comment (`/// <summary>`) and
  attributes: a method, constructor, property, class, record, struct, interface or enum.
  No `using` directives, no `namespace` declaration, no other members.
- A member (`Type.Method`) is written as it appears inside the type body, with its access
  modifier; it is placed inside the existing type.
- List the namespaces the code uses in `imports` as using directives, e.g. `using System.Linq;`.
- Match the interface exactly: modifiers (`public`, `static`, `async`, `override`), nullability
  annotations, generic constraints and the return type. Async methods return `Task`/`Task<T>`
  and end in `Async` when the project does so.
- Throw specific exceptions (`ArgumentNullException`, `InvalidOperationException`) rather than
  `Exception`; accept a `CancellationToken` when the interface has one.
- The code must build with `dotnet build`: use only types and members that exist in the context
  you were given.
