// Package graph connects foreman to an external code graph. foreman only needs two things
// from it: reindexing after commits and inbound references before deleting a symbol.
// Claude reads the graph through the graph's own MCP server.
package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
)

// Ref is one place that uses a symbol.
type Ref struct {
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	Relation string `json:"relation,omitempty"`
}

func (r Ref) String() string {
	s := r.File
	if r.Line > 0 {
		s += ":" + strconv.Itoa(r.Line)
	}
	if r.Symbol != "" {
		s += " " + r.Symbol
	}
	if r.Relation != "" {
		s += " [" + r.Relation + "]"
	}
	return s
}

// Provider is a code graph backend.
type Provider interface {
	Name() string
	// Available reports whether the graph can be used, and why not.
	Available(ctx context.Context) (bool, string)
	// Reindex refreshes the graph for changed files. Implementations may work in the background.
	Reindex(ctx context.Context, files []string) error
	// References returns places that use symbol (qualified, e.g. "BookService.List") from file.
	References(ctx context.Context, file, symbol string) ([]Ref, error)
}

// ErrUnavailable means the graph cannot answer; callers fall back to a name search.
var ErrUnavailable = errors.New("code graph unavailable")

// New builds the configured provider for a project root.
func New(cfg config.Graph, root string, log *slog.Logger) Provider {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	switch cfg.Provider {
	case "codebase-memory":
		return newCodebaseMemory(cfg, root, log)
	case "graphify":
		return newGraphify(cfg, root, log)
	case "command":
		return &command{cfg: cfg, root: root, bg: newBackground(log)}
	default:
		return noop{}
	}
}

type noop struct{}

func (noop) Name() string                                              { return "noop" }
func (noop) Available(context.Context) (bool, string)                  { return false, "no code graph configured" }
func (noop) Reindex(context.Context, []string) error                   { return nil }
func (noop) References(context.Context, string, string) ([]Ref, error) { return nil, ErrUnavailable }

// lastName returns the last segment of a qualified name.
func lastName(symbol string) string { return symbol[strings.LastIndex(symbol, ".")+1:] }

// run executes a command in dir with optional stdin and returns stdout.
func run(ctx context.Context, dir string, stdin []byte, argv ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		return stdout.Bytes(), fmt.Errorf("%s: %w: %s", argv[0], err, msg)
	}
	return stdout.Bytes(), nil
}

// background runs one reindex at a time and coalesces requests that arrive meanwhile.
type background struct {
	mu      sync.Mutex
	running bool
	pending func(context.Context) error
	log     *slog.Logger
}

func newBackground(log *slog.Logger) *background { return &background{log: log} }

func (b *background) submit(job func(context.Context) error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		b.pending = job
		return
	}
	b.running = true
	go b.loop(job)
}

func (b *background) loop(job func(context.Context) error) {
	for job != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		if err := job(ctx); err != nil {
			b.log.Warn("graph reindex failed", "err", err)
		}
		cancel()
		b.mu.Lock()
		job, b.pending = b.pending, nil
		if job == nil {
			b.running = false
		}
		b.mu.Unlock()
	}
}

// wait blocks until no reindex is running (for tests and shutdown).
func (b *background) wait() {
	for {
		b.mu.Lock()
		r := b.running
		b.mu.Unlock()
		if !r {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitCtx blocks until no reindex is running or ctx is done.
func (b *background) waitCtx(ctx context.Context) error {
	for {
		b.mu.Lock()
		r := b.running
		b.mu.Unlock()
		if !r {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// refsFromJSON extracts references from arbitrary JSON output: every object with a file
// field becomes a Ref. Graph tools differ in field names, so several common ones are tried.
func refsFromJSON(data []byte, root string) ([]Ref, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	var out []Ref
	seen := map[string]bool{}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			if f := firstString(t, "file_path", "file", "source_file", "filePath", "path"); f != "" {
				r := Ref{File: relPath(root, f), Symbol: firstString(t, "qualified_name", "name", "function_name", "label", "symbol"),
					Line: firstInt(t, "line", "start_line", "startLine", "line_number"), Relation: firstString(t, "relation", "type", "edge_type")}
				if k := r.String(); !seen[k] {
					seen[k] = true
					out = append(out, r)
				}
			}
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return out, nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func firstInt(m map[string]any, keys ...string) int {
	for _, k := range keys {
		switch n := m[k].(type) {
		case float64:
			return int(n)
		case string:
			if i, err := strconv.Atoi(n); err == nil {
				return i
			}
		}
	}
	return 0
}

func relPath(root, f string) string {
	if filepath.IsAbs(f) {
		if rel, err := filepath.Rel(root, f); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(strings.TrimPrefix(f, "./"))
}

// refsFromLines parses "path:line: text" output.
func refsFromLines(data []byte, root string) []Ref {
	var out []Ref
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		n, _ := strconv.Atoi(parts[1])
		r := Ref{File: relPath(root, parts[0]), Line: n}
		if len(parts) == 3 {
			r.Symbol = strings.TrimSpace(parts[2])
		}
		out = append(out, r)
	}
	return out
}

// excludeSelf drops the symbol's own definition from references.
func excludeSelf(refs []Ref, file, symbol string) []Ref {
	out := refs[:0]
	for _, r := range refs {
		if r.File == file && (r.Symbol == symbol || r.Symbol == lastName(symbol) || strings.HasSuffix(r.Symbol, "."+symbol)) {
			continue
		}
		out = append(out, r)
	}
	return out
}
