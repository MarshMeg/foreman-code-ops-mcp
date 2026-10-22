# Code graph

foreman uses an external code graph for three things: giving Claude the structure of the
codebase through the `graph_*` tools (graphify only), finding inbound references before
`delete_symbol` removes a symbol, and refreshing the graph after each commit.

```yaml
graph:
  provider: graphify            # graphify | codebase-memory | command | noop
  command: ""                   # executable (default per provider)
  project: ""                   # codebase-memory project name
  auto_index: false             # the graph refreshes itself; skip reindex after commits
```

When the graph cannot answer (not installed, project not indexed, stale) or finds no references,
`delete_symbol` also runs a whole-word name search and says so in its warnings: graphs built from
syntax miss some uses. `status` reports whether the graph is available and why not.

## graphify (default)

[graphify](https://pypi.org/project/graphifyy/) builds the graph from the code with tree-sitter.
Install it once per machine:

```bash
uv tool install graphifyy
```

foreman runs graphify itself, always with `GRAPHIFY_BACKEND=none`, so no LLM or API key is used
and only code is indexed: `foreman-code-ops init` builds `graphify-out/` (and ignores it in git),
foreman runs `graphify update .` in the background after each of its commits, and every graph
query first rebuilds the graph when it is missing or older than the last commit (commits made
outside foreman). Set `graph.auto_index: true` when something else keeps the graph fresh, for
example `graphify hook install`. Do not run `graphify install` or `graphify claude install`:
Claude reads the graph through foreman.

Tools for Claude, all answering in graphify's text format:

| Tool | graphify command | Answers |
|---|---|---|
| `graph_overview` | `GRAPH_REPORT.md` | size, core abstractions (most connected nodes), communities, surprising connections, import cycles |
| `graph_query {question, seeds?, context?, dfs?, budget?}` | `graphify query` | the symbols relevant to a question with `file:Lline` and edges |
| `graph_node {name, all?}` | `graphify explain` | one node and its connections; candidates when the name is ambiguous |
| `graph_path {from, to, undirected?}` | `graphify path` | the shortest path between two nodes |
| `graph_affected {name, depth?, relations?}` | `graphify affected` | what depends on a node, transitively |

References for `delete_symbol` come from `graphify-out/graph.json`: incoming edges (`calls`,
`uses`, `references`, `inherits`, `implements`, ...) to the symbol's node; structural edges such
as `method` or `contains` are ignored, inferred edges are marked as such.

## codebase-memory-mcp

foreman calls the CLI with JSON arguments on stdin and `--format json`:

- references: `codebase-memory-mcp cli trace_path` with
  `{"project": ..., "function_name": <last name segment>, "direction": "inbound", "depth": 1}`;
- reindex: `codebase-memory-mcp cli index_repository` with `{"repo_path": <root>, "mode": "fast"}`,
  in the background, one run at a time.

The project is found with `list_projects` by its path; set `graph.project` if that fails.
Repositories indexed from the CLI are not watched by codebase-memory, so foreman reindexes after
its commits. If codebase-memory's watcher or `auto_index` keeps the project fresh, set
`graph.auto_index: true`.

The `graph_*` tools are not available with codebase-memory; register codebase-memory-mcp in
Claude Code as well, so Claude can query the graph through its own tools.

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
