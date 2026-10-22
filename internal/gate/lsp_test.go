package gate

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MarshMeg/foreman-code-ops-mcp/adapters"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang/builtin"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lsp"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/project"
	tu "github.com/MarshMeg/foreman-code-ops-mcp/internal/testutil"
)

// With language servers on, an error in a changed file comes from the warm server and the
// check commands are skipped; a clean candidate still goes through the commands.
func TestLSPFirstStage(t *testing.T) {
	for _, c := range []struct {
		fixture, file, from, to, want, cliStep string
		tools                                  []string
	}{
		{"py-layered", "app/service.py", "return len(self.repo.list(BookFilter(limit=1000)))", `return "many"`,
			"is not assignable", ".: ruff", []string{"pyright-langserver", "ruff", "pyright"}},
		{"go-layered", "internal/service/book.go", "func (s *BookService) Count(ctx context.Context) (int, error) {",
			"func (s *BookService) Count(ctx context.Context) (int, error) {\n\tvar unused int", "declared and not used", ".: go build", []string{"gopls", "go"}},
	} {
		t.Run(c.fixture, func(t *testing.T) {
			g, root := gateFor(t, c.fixture, c.tools...)
			pool := lsp.NewPool(time.Minute)
			defer pool.Close()
			g.SetLSP(pool)
			ctx := context.Background()

			rep, err := g.Check(ctx, replaceIn(t, root, c.file, c.from, c.to), nil)
			if err != nil {
				t.Fatal(err)
			}
			if rep.OK || len(rep.Checked) != 1 || !strings.HasSuffix(rep.Checked[0], "(lsp)") {
				t.Fatalf("broken candidate: %+v", rep)
			}
			d := rep.Diagnostics[0]
			if d.File != c.file || d.Line == 0 || !strings.Contains(d.Message, c.want) || !strings.HasSuffix(d.Source, "(lsp)") {
				t.Fatalf("diagnostic %+v", d)
			}

			// A clean change: the server finds nothing, so the commands run as well.
			rep, err = g.Check(ctx, replaceIn(t, root, c.file, c.from, c.from+"\n"), nil)
			if err != nil {
				t.Fatal(err)
			}
			if !rep.OK || !strings.Contains(strings.Join(rep.Checked, ";"), c.cliStep) {
				t.Fatalf("clean candidate: %+v", rep)
			}
		})
	}
}

// A server that dies at startup only adds a warning; the check commands still decide.
func TestLSPBrokenServerFallsBack(t *testing.T) {
	tu.RequireTools(t, "go", "false")
	dir := tu.RepoFromFixture(t, "../../testdata/go-layered")
	own := filepath.Join(dir, ".foreman", "adapters", "go")
	if err := os.CopyFS(own, mustSub(t, adapters.FS, "go")); err != nil {
		t.Fatal(err)
	}
	yml := tu.ReadFile(t, dir, ".foreman/adapters/go/adapter.yaml")
	tu.WriteFile(t, dir, ".foreman/adapters/go/adapter.yaml", strings.Replace(yml, "run: [gopls]", "run: [\"false\"]", 1))
	reg, _, err := builtin.RegistryFor(dir)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(proj, reg, config.Default().Limits)
	if err != nil {
		t.Fatal(err)
	}
	pool := lsp.NewPool(time.Minute)
	defer pool.Close()
	g.SetLSP(pool)

	rep, err := g.Check(context.Background(), replaceIn(t, proj.Root, "internal/service/book.go", "return len(books), err", "return len(books), missing"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || !strings.Contains(strings.Join(rep.Warnings, "\n"), "lsp gopls skipped") || rep.Checked[0] != ".: go build" {
		t.Fatalf("report = %+v", rep)
	}
}

func mustSub(t *testing.T, fsys fs.FS, dir string) fs.FS {
	t.Helper()
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}
