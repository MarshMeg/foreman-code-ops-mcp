package graph

import "strings"

// dropSection removes the "## " sections whose heading starts with prefix.
func dropSection(md, prefix string) string {
	lines := strings.Split(md, "\n")
	var skip bool
	var kept []string
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			skip = strings.HasPrefix(line, prefix)
		}
		if !skip {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// Query is a graph question.
type Query struct {
	Question string
	Seeds    []string // start nodes instead of the question's keyword matches
	Context  []string // edge-context filter, e.g. call
	DFS      bool     // depth-first instead of breadth-first
	Budget   int      // output cap in tokens; 0 uses graphify's default
}
