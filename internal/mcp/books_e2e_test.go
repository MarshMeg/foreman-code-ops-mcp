package mcp

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
	tu "github.com/MarshMeg/foreman-code-ops-mcp/internal/testutil"
)

// TestE2EBookSearchEndpoint builds a "search books" endpoint across the three layers of the
// fixture the way Claude would: style guide, data layer, service, transport, then check.
func TestE2EBookSearchEndpoint(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH")
	}
	dir := tu.RepoFromFixture(t, "../../testdata/go-layered")

	code := map[string]tu.Answer{
		"`BookRepo`": {Code: `// BookRepo stores books.
type BookRepo interface {
	List(ctx context.Context, f domain.BookFilter) ([]domain.Book, error)
	Search(ctx context.Context, title string) ([]domain.Book, error)
}`},
		"`memRepo.Search`": {Code: `// Search returns books whose title contains title, case-insensitively.
func (r *memRepo) Search(ctx context.Context, title string) ([]domain.Book, error) {
	needle := strings.ToLower(title)
	var out []domain.Book
	for _, b := range r.books {
		if strings.Contains(strings.ToLower(b.Title), needle) {
			out = append(out, b)
		}
	}
	return out, nil
}`, Imports: []string{`"strings"`}},
		"`BookService.Search`": {Code: `// Search finds books by title.
func (s *BookService) Search(ctx context.Context, title string) ([]domain.Book, error) {
	books, err := s.repo.Search(ctx, title)
	if err != nil {
		return nil, fmt.Errorf("search books: %w", err)
	}
	return books, nil
}`},
		"`BookHandler.Search`": {Code: `// Search handles GET /books/search?q=.
func (h *BookHandler) Search(w http.ResponseWriter, r *http.Request) {
	books, err := h.svc.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(books)
}`},
	}
	gen := tu.NewFakeLLM(t)
	gen.Respond = func(prompt string) tu.Answer {
		for key, a := range code {
			if strings.Contains(prompt, "Write the") && strings.Contains(prompt, key+" in") {
				return a
			}
		}
		t.Errorf("unexpected generator prompt:\n%s", prompt)
		return tu.Answer{}
	}
	val := tu.NewFakeLLM(t)
	for range code {
		val.AddRaw(tu.Verdict(true, "none", nil, nil))
	}
	cs := connectProject(t, dir, func(c *config.Config) {
		c.Generator.BaseURL, c.Generator.Model = gen.URL, "local-model"
		c.Validator.Provider, c.Validator.BaseURL, c.Validator.Model = "openai", val.URL, "reviewer"
	})

	style := decode[ChangeOutput](t, call(t, cs, "style_set", map[string]any{
		"content": "# Style\n\n## Errors\nWrap errors with fmt.Errorf(\"<operation>: %w\", err).\n\n## Go\nAccept context.Context first.\n",
	}))
	if style.Status != "applied" {
		t.Fatalf("style_set = %+v", style)
	}

	steps := []map[string]any{
		// The implementation comes before the interface method, or the build breaks.
		{"file": "internal/repo/book.go", "symbol": "memRepo.Search", "kind": "method", "anchor": "memRepo.List",
			"spec": "Case-insensitive substring match on Title.", "interface": "func (r *memRepo) Search(ctx context.Context, title string) ([]domain.Book, error)",
			"context": []map[string]any{{"file": "internal/repo/book.go", "symbol": "memRepo"}, {"file": "internal/domain/book.go", "symbol": "Book"}}},
		{"file": "internal/repo/book.go", "symbol": "BookRepo", "kind": "interface",
			"spec": "Add Search(ctx, title) returning books whose title contains title.", "interface": "Search(ctx context.Context, title string) ([]domain.Book, error)"},
		{"file": "internal/service/book.go", "symbol": "BookService.Search", "kind": "method", "anchor": "BookService.List",
			"spec": "Delegate to the repository; wrap errors.", "interface": "func (s *BookService) Search(ctx context.Context, title string) ([]domain.Book, error)",
			"context": []map[string]any{{"file": "internal/repo/book.go", "symbol": "BookRepo"}}},
		{"file": "internal/transport/http.go", "symbol": "BookHandler.Search", "kind": "method",
			"spec": "GET /books/search?q=: call the service, write JSON, 500 on error.", "interface": "func (h *BookHandler) Search(w http.ResponseWriter, r *http.Request)",
			"context": []map[string]any{{"file": "internal/service/book.go", "symbol": "BookService.Search"}}},
	}
	for _, s := range steps {
		out := decode[UpsertSymbolOutput](t, call(t, cs, "upsert_symbol", s))
		if out.Status != "applied" || out.WrittenBy != "local" || out.ReviewedBy != "openai/reviewer" || out.Signature == "" {
			t.Fatalf("%s: %+v", s["symbol"], out)
		}
	}

	ch := decode[CheckOutput](t, call(t, cs, "check", map[string]any{}))
	if !ch.OK {
		t.Fatalf("check = %+v", ch)
	}
	log := tu.Git(t, dir, "log", "--format=%s|%(trailers:key=Foreman-Reviewed-By,valueonly,separator=)", "-5")
	for _, want := range []string{
		"foreman: upsert internal/transport/http.go BookHandler.Search|openai/reviewer",
		"foreman: upsert internal/service/book.go BookService.Search|openai/reviewer",
		"foreman: upsert internal/repo/book.go BookRepo|openai/reviewer",
		"foreman: upsert internal/repo/book.go memRepo.Search|openai/reviewer",
		"foreman: style: update style guide|",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("git log lacks %q:\n%s", want, log)
		}
	}
	if st := tu.Git(t, dir, "status", "--porcelain"); st != "" {
		t.Errorf("tree not clean:\n%s", st)
	}
	// The style guide reached the generator.
	if p := gen.Prompts()[0]; !strings.Contains(p, "## Project style guide") || !strings.Contains(p, "Wrap errors") {
		t.Error("style guide missing from the generator prompt")
	}
}
