package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tu "github.com/MarshMeg/foreman-code-ops/internal/testutil"
)

// TestRealServers runs the client against installed language servers: a candidate with a
// type error must be reported at the right line, the clean file must not, and the file on
// disk must stay as it was.
func TestRealServers(t *testing.T) {
	for _, c := range []struct {
		name, tool, fixture, file, lang, from, to, want string
		argv                                            []string
	}{
		{"pyright", "pyright-langserver", "py-layered", "app/service.py", "python",
			"return len(self.repo.list(BookFilter(limit=1000)))", `return "many"`, "is not assignable", []string{"pyright-langserver", "--stdio"}},
		{"gopls", "gopls", "go-layered", "internal/service/book.go", "go",
			"func (s *BookService) Count(ctx context.Context) (int, error) {", "func (s *BookService) Count(ctx context.Context) (int, error) {\n\tvar unused int", "declared and not used", []string{"gopls"}},
		// TypeScript 7 serves LSP itself; older versions go through typescript-language-server.
		{"tsc-lsp", "tsc", "ts-layered", "src/service/book.ts", "typescript",
			"return books.length;", `return "many";`, "not assignable", []string{"tsc", "--lsp", "--stdio"}},
		{"tsserver", "typescript-language-server", "ts-layered", "src/service/book.ts", "typescript",
			"return books.length;", `return "many";`, "not assignable", []string{"typescript-language-server", "--stdio"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			tu.RequireTools(t, c.tool)
			switch c.name {
			case "tsc-lsp":
				// Only TypeScript 7 (the native compiler) has --lsp.
				if out, _ := exec.Command("tsc", "--version").Output(); !strings.Contains(string(out), "Version 7.") {
					t.Skipf("tsc is not TypeScript 7: %s", strings.TrimSpace(string(out)))
				}
			case "tsserver":
				// typescript-language-server drives tsserver, which TypeScript 7 no longer ships.
				tu.RequireTools(t, "tsserver")
			}
			dir := t.TempDir()
			if err := os.CopyFS(dir, os.DirFS(filepath.Join("../../testdata", c.fixture))); err != nil {
				t.Fatal(err)
			}
			var initOptions map[string]any
			path := filepath.Join(dir, c.file)
			orig, _ := os.ReadFile(path)
			if !strings.Contains(string(orig), c.from) {
				t.Fatalf("fixture text %q not found", c.from)
			}
			candidate := strings.Replace(string(orig), c.from, c.to, 1)

			pool := NewPool(time.Minute)
			defer pool.Close()
			srv := Server{Argv: c.argv, Dir: dir, Env: os.Environ(), InitOptions: initOptions}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			start := time.Now()
			ds, err := pool.Diagnose(ctx, srv, []Document{{Path: path, LanguageID: c.lang, Text: []byte(candidate)}}, 500*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			first := time.Since(start)
			var hit *Diagnostic
			for i, d := range ds {
				if d.Severity == 1 && strings.Contains(d.Message, c.want) {
					hit = &ds[i]
				}
			}
			if hit == nil {
				t.Fatalf("no error containing %q in %+v", c.want, ds)
			}
			wantLine := strings.Count(candidate[:strings.Index(candidate, c.to)], "\n") + 1 + strings.Count(c.to, "\n")
			if hit.Line != wantLine || hit.File != path {
				t.Errorf("error at %s:%d, want line %d", hit.File, hit.Line, wantLine)
			}

			// The warm server sees the original content again, and faster.
			start = time.Now()
			ds, err = pool.Diagnose(ctx, srv, []Document{{Path: path, LanguageID: c.lang, Text: orig}}, 500*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range ds {
				if d.Severity == 1 {
					t.Errorf("clean file reported %+v", d)
				}
			}
			t.Logf("%s: first %v, warm %v, pull=%v", c.name, first.Round(time.Millisecond), time.Since(start).Round(time.Millisecond), pool.servers[dir+"\x00"+strings.Join(c.argv, "\x00")].c.pull)
			if now, _ := os.ReadFile(path); string(now) != string(orig) {
				t.Error("file on disk changed")
			}
		})
	}
}
