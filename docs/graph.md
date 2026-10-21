# Code graph

foreman uses an external code graph for two things only: refreshing it after each commit and
finding inbound references before `delete_symbol` removes a symbol. Claude itself reads the graph
through the graph's own MCP server.

```yaml
graph:
  provider: codebase-memory     # codebase-memory | graphify | command | noop
  command: ""                   # executable (default per provider)
  project: ""                   # codebase-memory project name
  auto_index: false             # the graph refreshes itself; skip reindex after commits
```

When the graph cannot answer (not installed, project not indexed, stale), `delete_symbol` falls
back to a whole-word name search and says so in its warnings. `status` reports whether the graph
is available and why not.

## codebase-memory-mcp (default)

foreman calls the CLI with JSON arguments on stdin and `--format json`:

- references: `codebase-memory-mcp cli trace_path` with
  `{"project": ..., "function_name": <last name segment>, "direction": "inbound", "depth": 1}`;
- reindex: `codebase-memory-mcp cli index_repository` with `{"repo_path": <root>, "mode": "fast"}`,
  in the background, one run at a time.

The project is found with `list_projects` by its path; set `graph.project` if that fails.
Repositories indexed from the CLI are not watched by codebase-memory, so foreman reindexes after
its commits. If codebase-memory's watcher or `auto_index` keeps the project fresh, set
`graph.auto_index: true`.

Register codebase-memory-mcp in Claude Code as well, so Claude can query the graph.

## graphify

foreman reads `graphify-out/graph.json` and follows incoming edges (`calls`, `uses`,
`references`, `inherits`, `implements`, ...) to the symbol's node; structural edges such as
`method` or `contains` are ignored. Inferred edges are marked as such.

After commits foreman runs `graphify update . --no-viz` unless `graph.auto_index: true`; set it
when `graphify hook install` already rebuilds the graph on every commit.

## command

Any other tool can be plugged in with argv templates:

```yaml
graph:
  provider: command
  references: [mytool, refs, "{name}", --file, "{file}"]
  reindex: [mytool, index, "{root}"]
```

Placeholders: `{root}`, `{file}`, `{symbol}` (qualified), `{name}` (last segment), `{project}`,
`{files}` (changed files, expanded to several arguments). `references` prints JSON (any object
with a `file`/`file_path`/`source_file` field becomes a reference) or `path:line: text` lines.

## noop

No graph; only the name search.
