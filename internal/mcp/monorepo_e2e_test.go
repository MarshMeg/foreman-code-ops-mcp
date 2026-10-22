package mcp

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
	tu "github.com/MarshMeg/foreman-code-ops/internal/testutil"
)

// TestE2EMonorepoLinks: a Go backend, a TSX frontend and a Python worker. Links are discovered,
// protect a handler from deletion, carry backend code into a frontend task as context, and
// stay current in the same commit as each change.
func TestE2EMonorepoLinks(t *testing.T) {
	tu.RequireTools(t, "tsc")
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH")
	}
	dir := tu.RepoFromFixture(t, "../../testdata/monorepo")

	gen := tu.NewFakeLLM(t)
	gen.Respond = func(p string) tu.Answer {
		switch {
		case strings.Contains(p, "`useBook` in"):
			if !strings.Contains(p, "## Context: backend/handler.go:BookHandler.Get") {
				t.Error("backend handler missing from the frontend task context")
			}
			return tu.Answer{Code: "/** Loads one book. */\nexport async function useBook(id: string): Promise<Book> {\n  const res = await fetch(`${API}/books/${id}`);\n  return (await res.json()) as Book;\n}"}
		case strings.Contains(p, "`BookHandler.Delete` in"):
			return tu.Answer{Code: "// Delete handles DELETE /api/books/{id}.\nfunc (h *BookHandler) Delete(w http.ResponseWriter, r *http.Request) {\n\tw.WriteHeader(http.StatusNoContent)\n}"}
		case strings.Contains(p, "`routes` in"):
			return tu.Answer{Code: "func routes(h *BookHandler) http.Handler {\n\tmux := http.NewServeMux()\n\tmux.HandleFunc(\"GET /api/books\", h.List)\n\tmux.HandleFunc(\"GET /api/books/{id}\", h.Get)\n\tmux.HandleFunc(\"POST /api/books\", h.Create)\n\tmux.HandleFunc(\"DELETE /api/books/{id}\", h.Delete)\n\treturn mux\n}"}
		}
		t.Errorf("unexpected prompt:\n%s", p)
		return tu.Answer{}
	}
	cs := connectProject(t, dir, func(c *config.Config) {
		c.Generator.BaseURL, c.Generator.Model, c.Validator.Provider = gen.URL, "m", "none"
	})

	disc := decode[DiscoverLinksOutput](t, call(t, cs, "discover_links", map[string]any{}))
	if disc.Status != "applied" || disc.Routes != 3 || len(disc.Links) != 3 || strings.Join(disc.Services, ",") != "backend,frontend,worker" {
		t.Fatalf("discover_links = %+v", disc)
	}

	got := decode[GetLinksOutput](t, call(t, cs, "get_links", map[string]any{"symbol": "useBooks"}))
	if len(got.Links) != 1 || got.Links[0].To.Symbol != "BookHandler.List" || got.Links[0].Confidence != "contract" {
		t.Fatalf("get_links = %+v", got)
	}

	del := decode[ChangeOutput](t, call(t, cs, "delete_symbol", map[string]any{"file": "backend/handler.go", "symbol": "BookHandler.List"}))
	if del.Status != "refused" || !strings.Contains(strings.Join(del.References, "\n"), "called from another service: frontend.useBooks") {
		t.Fatalf("delete_symbol = %+v", del)
	}

	up := decode[UpsertSymbolOutput](t, call(t, cs, "upsert_symbol", map[string]any{
		"file": "frontend/src/hooks/useBooks.tsx", "symbol": "useBook", "kind": "function",
		"spec": "Fetch one book by id from the backend.", "interface": "export async function useBook(id: string): Promise<Book>",
		"context": []map[string]any{{"file": "backend/handler.go", "symbol": "BookHandler.Get"}, {"file": "frontend/src/api.ts", "symbol": "Book"}},
	}))
	if up.Status != "applied" {
		t.Fatalf("upsert useBook = %+v", up)
	}
	if files := tu.Git(t, dir, "show", "--name-only", "--format=", "HEAD"); files != ".foreman/links.json\nfrontend/src/hooks/useBooks.tsx\n" {
		t.Fatalf("links.json not in the same commit:\n%s", files)
	}
	got = decode[GetLinksOutput](t, call(t, cs, "get_links", map[string]any{"symbol": "useBook"}))
	if len(got.Links) != 1 || got.Links[0].To.Symbol != "BookHandler.Get" {
		t.Fatalf("get_links useBook = %+v", got)
	}

	for _, s := range []map[string]any{
		{"file": "backend/handler.go", "symbol": "BookHandler.Delete", "kind": "method", "spec": "Delete a book; 204.", "interface": "func (h *BookHandler) Delete(w http.ResponseWriter, r *http.Request)"},
		{"file": "backend/main.go", "symbol": "routes", "kind": "function", "spec": "Register DELETE /api/books/{id}.", "interface": "func routes(h *BookHandler) http.Handler"},
	} {
		if out := decode[UpsertSymbolOutput](t, call(t, cs, "upsert_symbol", s)); out.Status != "applied" {
			t.Fatalf("%s: %+v", s["symbol"], out)
		}
	}
	links := tu.ReadFile(t, dir, ".foreman/links.json")
	if !strings.Contains(links, `"method": "DELETE"`) || !strings.Contains(links, `"symbol": "BookHandler.Delete"`) {
		t.Fatalf("new route missing from links.json:\n%s", links)
	}
	if st := tu.Git(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("tree not clean:\n%s", st)
	}
}
