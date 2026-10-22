package setup

// claudeSection is the CLAUDE.md section init writes between its markers: use foreman's graph to
// read the code and foreman's tools to change it.
func claudeSection() string {
	const bt = "`"
	text := "<!-- foreman-code-ops:begin -->\n" +
		"## foreman-code-ops\n" +
		"\n" +
		"This project is connected to the foreman MCP server (tools " + bt + "mcp__foreman__*" + bt + ").\n" +
		"\n" +
		"- **Explore the code through foreman's graph first.** Start with " + bt + "graph_overview" + bt + " (structure, core abstractions, communities). " +
		"Find code with " + bt + "graph_query" + bt + ", inspect a symbol with " + bt + "graph_node" + bt + ", " +
		"trace dependencies with " + bt + "graph_path" + bt + " and the impact of a change with " + bt + "graph_affected" + bt + "; " +
		"then read the exact source with " + bt + "get_symbol" + bt + ". " +
		"Read or grep whole files only when the graph does not answer.\n" +
		"- **Write code through foreman.** Create, change and delete functions, methods, types and components with " +
		bt + "upsert_symbol" + bt + ", " + bt + "delete_symbol" + bt + ", " + bt + "create_file" + bt + " and " + bt + "delete_file" + bt + ", " +
		"not Edit or Write: a cheaper model writes the code, and foreman builds, lints, reviews and commits it. " +
		"The foreman-code-ops skill explains how.\n" +
		"- Call " + bt + "status" + bt + " at the start of a session; check with " + bt + "check" + bt + " and " + bt + "run_tests" + bt + ".\n" +
		"- Write specs, interfaces, commit messages and the style guide (" + bt + "style_set" + bt + ") in English.\n" +
		"<!-- foreman-code-ops:end -->\n"
	return text
}
