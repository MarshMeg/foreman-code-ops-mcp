package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
)

// codebaseMemory uses the codebase-memory-mcp CLI: `codebase-memory-mcp cli <tool>` with
// the tool's JSON arguments on stdin and `--format json` output.
type codebaseMemory struct {
	cfg  config.Graph
	root string
	bin  string
	bg   *background

	mu      sync.Mutex
	project string
}

func newCodebaseMemory(cfg config.Graph, root string, log *slog.Logger) *codebaseMemory {
	bin := cfg.Command
	if bin == "" {
		bin = "codebase-memory-mcp"
	}
	return &codebaseMemory{cfg: cfg, root: root, bin: bin, bg: newBackground(log), project: cfg.Project}
}

func (c *codebaseMemory) Name() string { return "codebase-memory" }

func (c *codebaseMemory) Available(ctx context.Context) (bool, string) {
	if _, err := exec.LookPath(c.bin); err != nil {
		return false, c.bin + " not found in PATH"
	}
	if _, err := c.projectName(ctx); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func (c *codebaseMemory) tool(ctx context.Context, name string, args any) ([]byte, error) {
	in, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return run(ctx, c.root, in, c.bin, "cli", name, "--format", "json", "--quiet")
}

// projectName returns the configured project, or finds the indexed project whose path is the root.
func (c *codebaseMemory) projectName(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.project != "" {
		return c.project, nil
	}
	out, err := run(ctx, c.root, nil, c.bin, "cli", "list_projects", "--format", "json", "--quiet")
	if err != nil {
		return "", fmt.Errorf("%w: list_projects: %w", ErrUnavailable, err)
	}
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("%w: list_projects output: %w", ErrUnavailable, err)
	}
	if name := findProject(v, c.root); name != "" {
		c.project = name
		return name, nil
	}
	return "", fmt.Errorf("%w: %s is not indexed; run `%s cli index_repository` with {\"repo_path\": %q} or set graph.project", ErrUnavailable, c.root, c.bin, c.root)
}

// findProject looks for an object with a path field equal to root and returns its name.
func findProject(v any, root string) string {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			if n := findProject(e, root); n != "" {
				return n
			}
		}
	case map[string]any:
		for _, k := range []string{"root_path", "repo_path", "path", "root"} {
			if p, ok := t[k].(string); ok && filepath.Clean(p) == root {
				if n := firstString(t, "name", "project"); n != "" {
					return n
				}
			}
		}
		for _, e := range t {
			if n := findProject(e, root); n != "" {
				return n
			}
		}
	}
	return ""
}

// Reindex re-runs index_repository in the background. Repositories indexed from the CLI are
// not watched by codebase-memory, so this keeps the graph current after foreman's commits.
func (c *codebaseMemory) Reindex(_ context.Context, _ []string) error {
	if c.cfg.AutoIndex {
		return nil
	}
	if _, err := exec.LookPath(c.bin); err != nil {
		return nil
	}
	c.bg.submit(func(ctx context.Context) error {
		args := map[string]any{"repo_path": c.root, "mode": "fast"}
		if c.cfg.Project != "" {
			args["name"] = c.cfg.Project
		}
		_, err := c.tool(ctx, "index_repository", args)
		return err
	})
	return nil
}

func (c *codebaseMemory) References(ctx context.Context, file, symbol string) ([]Ref, error) {
	if _, err := exec.LookPath(c.bin); err != nil {
		return nil, fmt.Errorf("%w: %s not found", ErrUnavailable, c.bin)
	}
	project, err := c.projectName(ctx)
	if err != nil {
		return nil, err
	}
	out, err := c.tool(ctx, "trace_path", map[string]any{
		"project": project, "function_name": lastName(symbol), "direction": "inbound", "depth": 1,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: trace_path: %w", ErrUnavailable, err)
	}
	refs, err := refsFromJSON(out, c.root)
	if err != nil {
		return nil, fmt.Errorf("%w: trace_path output: %w", ErrUnavailable, err)
	}
	return excludeSelf(refs, file, symbol), nil
}

// graphify reads graphify-out/graph.json (NetworkX node-link format).
type graphify struct {
	cfg  config.Graph
	root string
	bin  string
	bg   *background
}

func newGraphify(cfg config.Graph, root string, log *slog.Logger) *graphify {
	bin := cfg.Command
	if bin == "" {
		bin = "graphify"
	}
	return &graphify{cfg: cfg, root: root, bin: bin, bg: newBackground(log)}
}

func (g *graphify) Name() string { return "graphify" }

func (g *graphify) path() string { return filepath.Join(g.root, "graphify-out", "graph.json") }

// reportPath returns the location of the human-readable report that graphify writes next to graph.json.
func (g *graphify) reportPath() string {
	return filepath.Join(g.root, "graphify-out", "GRAPH_REPORT.md")
}

// headTime returns the time of the last commit, or the zero time.
func (g *graphify) headTime(ctx context.Context) time.Time {
	out, err := run(ctx, g.root, nil, "git", "log", "-1", "--format=%ct")
	if err != nil {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

func (g *graphify) Available(context.Context) (bool, string) {
	if _, err := os.Stat(g.path()); err != nil {
		return false, "graphify-out/graph.json not found; run graphify on the project"
	}
	return true, ""
}

// Reindex runs `graphify update .` in the background unless graphify's own git hook rebuilds the
// graph on commit.
func (g *graphify) Reindex(context.Context, []string) error {
	if g.cfg.AutoIndex {
		return nil
	}
	if _, err := exec.LookPath(g.bin); err != nil {
		return nil
	}
	g.bg.submit(func(ctx context.Context) error {
		_, _, err := g.cli(ctx, "update", ".")
		return err
	})
	return nil
}

type nodeLink struct {
	Nodes []map[string]any `json:"nodes"`
	Links []map[string]any `json:"links"`
	Edges []map[string]any `json:"edges"`
}

// structural relations describe containment or similarity, not use.
var structural = map[string]bool{"contains": true, "defines": true, "method": true, "semantically_similar_to": true}

func (g *graphify) References(_ context.Context, file, symbol string) ([]Ref, error) {
	data, err := os.ReadFile(g.path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: graphify-out/graph.json not found", ErrUnavailable)
	}
	if err != nil {
		return nil, err
	}
	var gr nodeLink
	if err := json.Unmarshal(data, &gr); err != nil {
		return nil, fmt.Errorf("%w: graph.json: %w", ErrUnavailable, err)
	}
	nodes := map[string]map[string]any{}
	targets := map[string]bool{}
	name := lastName(symbol)
	for _, n := range gr.Nodes {
		id := fmt.Sprint(n["id"])
		nodes[id] = n
		label := strings.TrimSuffix(firstString(n, "label", "name"), "()")
		src := relPath(g.root, firstString(n, "source_file"))
		if (label == symbol || label == name || strings.HasSuffix(label, "."+name)) && (src == "" || src == file) {
			targets[id] = true
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("%w: %s not found in graph.json (stale graph?)", ErrUnavailable, symbol)
	}
	var refs []Ref
	seen := map[string]bool{}
	for _, e := range append(gr.Links, gr.Edges...) {
		rel := firstString(e, "relation", "type")
		if !targets[fmt.Sprint(e["target"])] || structural[rel] {
			continue
		}
		src := nodes[fmt.Sprint(e["source"])]
		r := Ref{File: relPath(g.root, firstString(e, "source_file")), Symbol: firstString(src, "label", "name"), Relation: rel}
		if r.File == "" {
			r.File = relPath(g.root, firstString(src, "source_file"))
		}
		if c := firstString(e, "confidence"); c != "" && c != "EXTRACTED" {
			r.Relation += ", " + strings.ToLower(c)
		}
		if k := r.String(); !seen[k] {
			seen[k] = true
			refs = append(refs, r)
		}
	}
	return excludeSelf(refs, file, symbol), nil
}

// command runs user-configured argv templates (graph.reindex, graph.references).
type command struct {
	cfg  config.Graph
	root string
	bg   *background
}

func (c *command) Name() string { return "command" }

func (c *command) Available(context.Context) (bool, string) {
	if len(c.cfg.References) == 0 {
		return false, "graph.references is not set"
	}
	if _, err := exec.LookPath(c.cfg.References[0]); err != nil {
		return false, c.cfg.References[0] + " not found in PATH"
	}
	return true, ""
}

func (c *command) expand(argv []string, file, symbol string, files []string) []string {
	vars := map[string]string{"{root}": c.root, "{file}": file, "{symbol}": symbol, "{name}": lastName(symbol), "{project}": c.cfg.Project}
	var out []string
	for _, a := range argv {
		if a == "{files}" {
			out = append(out, files...)
			continue
		}
		for k, v := range vars {
			a = strings.ReplaceAll(a, k, v)
		}
		out = append(out, a)
	}
	return out
}

func (c *command) Reindex(_ context.Context, files []string) error {
	if c.cfg.AutoIndex || len(c.cfg.Reindex) == 0 {
		return nil
	}
	argv := c.expand(c.cfg.Reindex, "", "", files)
	c.bg.submit(func(ctx context.Context) error {
		_, err := run(ctx, c.root, nil, argv...)
		return err
	})
	return nil
}

func (c *command) References(ctx context.Context, file, symbol string) ([]Ref, error) {
	if ok, why := c.Available(ctx); !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, why)
	}
	out, err := run(ctx, c.root, nil, c.expand(c.cfg.References, file, symbol, nil)...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	refs, jerr := refsFromJSON(out, c.root)
	if jerr != nil {
		refs = refsFromLines(out, c.root)
	}
	return excludeSelf(refs, file, symbol), nil
}
