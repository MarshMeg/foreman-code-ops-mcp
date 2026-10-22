package graph

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
