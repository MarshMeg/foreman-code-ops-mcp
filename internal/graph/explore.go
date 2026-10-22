package graph

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

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

// Overview returns graphify's report (summary, core abstractions, communities) without the bare list of community names.
func (g *graphify) Overview(ctx context.Context) (string, error) {
	if err := g.fresh(ctx); err != nil {
		return "", err
	}
	data, err := os.ReadFile(g.reportPath())
	if err != nil {
		return "", fmt.Errorf("%w: graphify-out/GRAPH_REPORT.md: %w", ErrUnavailable, err)
	}
	// The community hubs section is a bare list of names and carries no information.
	report := dropSection(string(data), "## Community Hubs")
	const maxOverview = 24000
	if len(report) > maxOverview {
		report = report[:maxOverview] + "\n\n[truncated; use graph_query or graph_node for details]\n"
	}
	return report, nil
}

// cli runs graphify in the project root with LLM backends disabled. It returns stdout (with stderr
// for exit codes 3 and 4, where graphify lists candidates or reports no match) and the exit code.
func (g *graphify) cli(ctx context.Context, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, g.bin, args...)
	cmd.Dir = g.root
	// Foreman only needs the code graph, which graphify builds locally without an LLM.
	cmd.Env = append(os.Environ(), "GRAPHIFY_BACKEND=none")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), 0, nil
	}
	code := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	if code == 3 || code == 4 {
		return strings.TrimSpace(stdout.String() + "\n" + stderr.String()), code, err
	}
	msg := strings.TrimSpace(stderr.String())
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	return "", code, fmt.Errorf("graphify %s: %w: %s", args[0], err, msg)
}

// Build creates or refreshes the graph synchronously (code only, no LLM).
func (g *graphify) Build(ctx context.Context) error {
	if _, err := exec.LookPath(g.bin); err != nil {
		return fmt.Errorf("%w: %s not found in PATH (install it: uv tool install graphifyy)", ErrUnavailable, g.bin)
	}
	// "update" builds graph.json and GRAPH_REPORT.md from scratch when they are missing.
	_, _, err := g.cli(ctx, "update", ".")
	return err
}

// ask runs a read-only graphify command on a fresh graph. Exit codes 3 (ambiguous name) and 4 (no
// match) still carry an answer.
func (g *graphify) ask(ctx context.Context, args ...string) (string, error) {
	if err := g.fresh(ctx); err != nil {
		return "", err
	}
	args = append(args, "--graph", g.path())
	out, code, err := g.cli(ctx, args...)
	if err == nil || code == 3 || code == 4 {
		return out, nil
	}
	return "", err
}

// Explain returns one node (source location, community) and its connections.
func (g *graphify) Explain(ctx context.Context, name string, all bool) (string, error) {
	args := []string{"explain", name}
	if all {
		args = append(args, "--all")
	}
	return g.ask(ctx, args...)
}

// Query returns the part of the graph relevant to a question.
func (g *graphify) Query(ctx context.Context, q Query) (string, error) {
	args := []string{"query", q.Question}
	if q.DFS {
		args = append(args, "--dfs")
	}
	for _, s := range q.Seeds {
		args = append(args, "--seed", s)
	}
	for _, c := range q.Context {
		args = append(args, "--context", c)
	}
	if q.Budget > 0 {
		args = append(args, "--budget", strconv.Itoa(q.Budget))
	}
	return g.ask(ctx, args...)
}

// fresh waits for a running reindex and rebuilds the graph when it is missing or older than the
// last commit, so commits made outside foreman are seen too.
func (g *graphify) fresh(ctx context.Context) error {
	if err := g.bg.waitCtx(ctx); err != nil {
		return err
	}
	st, statErr := os.Stat(g.path())
	if statErr == nil && !st.ModTime().Before(g.headTime(ctx)) {
		return nil
	}
	if err := g.Build(ctx); err != nil {
		// A stale graph is better than none.
		if statErr == nil {
			return nil
		}
		return err
	}
	return nil
}
