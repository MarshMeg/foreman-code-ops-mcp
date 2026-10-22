package mcp

import (
	"context"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/graph"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// GraphOutput is the result of the graph_* tools.
type GraphOutput struct {
	Text string `json:"text" jsonschema:"the code graph's answer"`
}

// GraphAffectedInput is the input of graph_affected.
type GraphAffectedInput struct {
	Name      string   `json:"name" jsonschema:"symbol, file path or node id whose dependents you want"`
	Depth     int      `json:"depth,omitempty" jsonschema:"how many hops back (default 2)"`
	Relations []string `json:"relations,omitempty" jsonschema:"edge relations to follow, e.g. calls, references, imports (default: all uses)"`
}

// GraphPathInput is the input of graph_path.
type GraphPathInput struct {
	From       string `json:"from" jsonschema:"start node: symbol, file path or node id"`
	To         string `json:"to" jsonschema:"end node"`
	Undirected bool   `json:"undirected,omitempty" jsonschema:"ignore edge direction"`
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

// explore runs a code graph question for the graph_* tools.
func (h *handlers) explore(fn func(graph.Explorer) (string, error)) (*sdk.CallToolResult, GraphOutput, error) {
	e, err := h.d.Ops.Explorer()
	if err != nil {
		return nil, GraphOutput{}, err
	}
	text, err := fn(e)
	if err != nil {
		return nil, GraphOutput{}, err
	}
	return nil, GraphOutput{Text: text}, nil
}

// graphPath handles graph_path: it returns the shortest path between two nodes.
func (h *handlers) graphPath(ctx context.Context, _ *sdk.CallToolRequest, in GraphPathInput) (*sdk.CallToolResult, GraphOutput, error) {
	return h.explore(func(e graph.Explorer) (string, error) {
		return e.Path(ctx, in.From, in.To, in.Undirected)
	})
}

// graphNode handles graph_node: it explains one node and its connections.
func (h *handlers) graphNode(ctx context.Context, _ *sdk.CallToolRequest, in GraphNodeInput) (*sdk.CallToolResult, GraphOutput, error) {
	return h.explore(func(e graph.Explorer) (string, error) {
		return e.Explain(ctx, in.Name, in.All)
	})
}

// graphQuery handles graph_query: it returns the part of the code graph relevant to a question.
func (h *handlers) graphQuery(ctx context.Context, _ *sdk.CallToolRequest, in GraphQueryInput) (*sdk.CallToolResult, GraphOutput, error) {
	return h.explore(func(e graph.Explorer) (string, error) {
		return e.Query(ctx, graph.Query{
			Question: in.Question,
			Seeds:    in.Seeds,
			Context:  in.Context,
			DFS:      in.DFS,
			Budget:   in.Budget,
		})
	})
}

// graphOverview handles graph_overview: it returns the project report with the summary, core
// abstractions and communities.
func (h *handlers) graphOverview(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, GraphOutput, error) {
	return h.explore(func(e graph.Explorer) (string, error) {
		return e.Overview(ctx)
	})
}
