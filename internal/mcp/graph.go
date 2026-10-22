package mcp

// GraphOutput is the result of the graph_* tools.
type GraphOutput struct {
	Text string `json:"text" jsonschema:"the code graph's answer"`
}
