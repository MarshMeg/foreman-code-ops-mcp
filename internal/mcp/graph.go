package mcp

// GraphOutput is the result of the graph_* tools.
type GraphOutput struct {
	Text string `json:"text" jsonschema:"the code graph's answer"`
}

// GraphNodeInput is the input of graph_node.
type GraphNodeInput struct {
	Name string `json:"name" jsonschema:"symbol, file path or node id; use path::symbol when a name is ambiguous"`
	All  bool   `json:"all,omitempty" jsonschema:"list every connection, not only the 20 most connected"`
}

// GraphQueryInput is the input of graph_query.
type GraphQueryInput struct {
	Question string   `json:"question" jsonschema:"what you want to know, e.g. how books are listed over HTTP"`
	Seeds    []string `json:"seeds,omitempty" jsonschema:"start nodes (symbol, file path or node id) instead of the question's keywords"`
	Context  []string `json:"context,omitempty" jsonschema:"edge-context filter, e.g. call, field, parameter_type"`
	DFS      bool     `json:"dfs,omitempty" jsonschema:"depth-first instead of breadth-first traversal"`
	Budget   int      `json:"budget,omitempty" jsonschema:"output cap in tokens (default 2000)"`
}
