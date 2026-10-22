package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/testutil"
)

// TestGraphifyExplorer runs the explorer against the real graphify CLI on the Go fixture.
func TestGraphifyExplorer(t *testing.T) {
	testutil.RequireTools(t, "graphify", "git")
	root := testutil.RepoFromFixture(t, "../../testdata/go-layered")
	p := New(config.Graph{Provider: "graphify"}, root, nil)
	e, ok := p.(Explorer)
	if !ok {
		t.Fatalf("provider %T does not implement Explorer", p)
	}
	ctx := context.Background()

	if avail, reason := p.Available(ctx); avail {
		t.Fatalf("Available before the first query = true (%s), want false", reason)
	}

	out, err := e.Explain(ctx, "BookService", false)
	if err != nil {
		t.Fatalf("Explain: %v\n%s", err, out)
	}
	if !strings.Contains(out, "internal/service/book.go") {
		t.Fatalf("Explain output lacks internal/service/book.go:\n%s", out)
	}
	if avail, reason := p.Available(ctx); !avail {
		t.Fatalf("Available after the first query = false (%s), want true", reason)
	}
	graphPath := filepath.Join(root, "graphify-out", "graph.json")
	if _, err := os.Stat(graphPath); err != nil {
		t.Fatalf("graph.json missing: %v", err)
	}

	report, err := e.Overview(ctx)
	if err != nil {
		t.Fatalf("Overview: %v\n%s", err, report)
	}
	if !strings.Contains(report, "## God Nodes") {
		t.Fatalf("Overview lacks God Nodes section:\n%s", report)
	}
	if strings.Contains(report, "## Community Hubs") {
		t.Fatalf("Overview contains Community Hubs section:\n%s", report)
	}

	out, err = e.Query(ctx, Query{Question: "list books", Budget: 500})
	if err != nil {
		t.Fatalf("Query: %v\n%s", err, out)
	}
	if !strings.Contains(out, "BookService") {
		t.Fatalf("Query output lacks BookService:\n%s", out)
	}

	out, err = e.Affected(ctx, "Book", 1, nil)
	if err != nil {
		t.Fatalf("Affected: %v\n%s", err, out)
	}
	if !strings.Contains(out, "internal/repo/book.go") {
		t.Fatalf("Affected output lacks internal/repo/book.go:\n%s", out)
	}

	out, err = e.Explain(ctx, "NoSuchSymbolAnywhere", false)
	if err != nil {
		t.Fatalf("Explain of a missing symbol returned an error: %v\n%s", err, out)
	}

	extra := filepath.Join(root, "internal", "extra.go")
	if err := os.WriteFile(extra, []byte("package internal\n\nfunc Extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", ".")
	testutil.Git(t, root, "commit", "-q", "-m", "extra")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(graphPath, old, old); err != nil {
		t.Fatal(err)
	}
	out, err = e.Explain(ctx, "Extra", false)
	if err != nil {
		t.Fatalf("Explain after commit: %v\n%s", err, out)
	}
	if !strings.Contains(out, "internal/extra.go") {
		t.Fatalf("Explain output lacks internal/extra.go after rebuild:\n%s", out)
	}
}
