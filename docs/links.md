# Links between services

In a monorepo, foreman maps who calls whom: `discover_links` scans every service declared in
`.foreman/workspace.yaml`, links client calls to the routes they reach and writes
`.foreman/links.json`. Commit it: afterwards every foreman change recomputes the map and commits
it together with the code.

```
frontend.useBooks → GET /api/books → backend.BookHandler.List [contract]
worker.fetch_book → GET /api/books/{} → backend.BookHandler.Get [contract]
```

## What is detected

| | Go | Python | TypeScript / JavaScript |
|---|---|---|---|
| Routes | `net/http` (incl. `"GET /path"` patterns), chi, gin, echo, fiber | FastAPI, Flask, Django `path()` | express, fastify, hono, NestJS |
| Router prefixes | `Group`, `Route(... func(r))`, `Mount` | `APIRouter(prefix=)`, `Blueprint(url_prefix=)`, `include_router`/`register_blueprint` (also across files) | `app.use("/api", router)` (also imported routers), `@Controller("books")` |
| Client calls | `http.Get/Post/Head`, `http.NewRequest[WithContext]` | `requests`, `httpx`, clients with `base_url` | `fetch`, axios / ky and their `create({baseURL})` instances |
| gRPC | `.proto` services become routes; calls to `<Service>Client` methods become calls | same | same |

Handlers are resolved to symbols: the decorated function (FastAPI, Flask, NestJS), the handler
argument (`h.List`, `listBooks`), or the enclosing function. Test files are skipped.

URLs are normalized: scheme and host dropped, parameters (`{id}`, `:id`, `<int:id>`, `${id}`,
`%d`, f-string fields) become `{}`, no trailing slash. A leading base URL expression
(`${API}`, `f"{BASE}..."`, `baseURL + ...`) is resolved to the service it points to:

- `consumes: [{service: backend, base_url_env: VITE_API_URL}]` in workspace.yaml;
- environment values in `docker-compose.yml` and `.env*` files: `VITE_API_URL=http://backend:8080/api`
  points to the compose service `backend` (matched to a workspace service by name or build
  context) and prepends `/api` to the call path;
- `import.meta.env.X`, `process.env.X`, `os.Getenv("X")`, `os.environ["X"]` and module-level
  constants holding them.

## Confidence

| Level | Meaning |
|---|---|
| `contract` | method and path match, and the server's OpenAPI (`exposes.openapi` or `openapi.yaml`/`swagger.*` in the service) or protobuf declares the operation |
| `matched` | method and path match |
| `guessed` | the path matches only after dropping leading segments, several services expose it with no known target, or the URL was mostly dynamic |

## Using links

- `get_links {service?, file?, symbol?}` returns the links touching them. Look before changing a
  handler, its DTOs or a client call.
- Pass the other side as `upsert_symbol` context: a frontend hook can get the backend handler and
  its response type, so the writer sees the real response shape.
- `delete_symbol` refuses to delete a handler that other services call (unless `force`).

## Not detected yet

Message topics (Kafka, NATS, RabbitMQ, Redis pub/sub), GraphQL schemas, and URLs assembled across
several functions. Calls that match no route are listed by `discover_links` as `unlinked`.
