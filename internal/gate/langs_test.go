package gate

import (
	"context"
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/lang/builtin"
	"github.com/MarshMeg/foreman-code-ops/internal/project"
	tu "github.com/MarshMeg/foreman-code-ops/internal/testutil"
	"github.com/MarshMeg/foreman-code-ops/internal/workspace"
)

func gateFor(t *testing.T, fixture string, tools ...string) (*Gate, string) {
	t.Helper()
	tu.RequireTools(t, tools...)
	dir := tu.RepoFromFixture(t, "../../testdata/"+fixture)
	proj, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := builtin.Registry()
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(proj, reg, config.Default().Limits)
	if err != nil {
		t.Fatal(err)
	}
	return g, proj.Root
}

func checked(rep Report) string { return strings.Join(rep.Checked, ";") }

func TestPythonGate(t *testing.T) {
	g, root := gateFor(t, "py-layered", "ruff", "pyright")
	ctx := context.Background()

	rep, err := g.Check(ctx, workspace.New(root), []string{"app"})
	if err != nil || !rep.OK {
		t.Fatalf("clean check: %+v %v", rep, err)
	}
	if checked(rep) != ".: ruff;.: pyright" {
		t.Fatalf("checked = %s", checked(rep))
	}

	// A candidate with an undefined name, an unused import and bad formatting: the fixers
	// clean imports and format, the checks report the undefined name at its real path.
	before := tu.ReadFile(t, root, "app/service.py")
	cs := replaceIn(t, root, "app/service.py", "    def count(self) -> int:\n        return len(self.repo.list(BookFilter(limit=1000)))",
		"    def count(self) -> int:\n        return  len(self.repo.list(BookFilter(limit=missing)))")
	diags, err := g.Prepare(ctx, cs, map[string][]string{"app/service.py": {"os", "from app.domain import Book"}})
	if err != nil || len(diags) != 0 {
		t.Fatalf("prepare: %v %v", diags, err)
	}
	src, _, _ := cs.Read("app/service.py")
	if strings.Contains(string(src), "import os") || strings.Contains(string(src), "return  len") {
		t.Errorf("not fixed:\n%s", src)
	}
	rep, err = g.Check(ctx, cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || rep.Diagnostics[0].File != "app/service.py" || !strings.Contains(rep.Diagnostics[0].Message, "missing") {
		t.Fatalf("report = %+v", rep)
	}
	if tu.ReadFile(t, root, "app/service.py") != before {
		t.Fatal("real file changed")
	}
}

func TestPythonTypeErrorCaughtByPyright(t *testing.T) {
	g, root := gateFor(t, "py-layered", "ruff", "pyright")
	cs := replaceIn(t, root, "app/service.py", "return len(self.repo.list(BookFilter(limit=1000)))", `return "many"`)
	rep, err := g.Check(context.Background(), cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || rep.Diagnostics[0].Source != "pyright" || rep.Diagnostics[0].File != "app/service.py" {
		t.Fatalf("report = %+v", rep)
	}
}

func TestPythonTests(t *testing.T) {
	g, root := gateFor(t, "py-layered", "ruff", "pytest")
	cs := replaceIn(t, root, "app/service.py", "return len(self.repo.list(BookFilter(limit=1000)))", "return 42")
	res, err := g.Test(context.Background(), cs, "app/service.py", "count")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ran || res.Passed || !strings.Contains(res.Output, "42") || strings.Contains(res.Output, "foreman-code-ops/shadow") {
		t.Fatalf("result = %+v", res)
	}
	res, _ = g.Test(context.Background(), workspace.New(root), "", "")
	if !res.Passed {
		t.Fatalf("clean run = %+v", res)
	}
}

func TestTypeScriptGate(t *testing.T) {
	g, root := gateFor(t, "ts-layered", "tsc", "prettier")
	ctx := context.Background()
	rep, err := g.Check(ctx, workspace.New(root), nil)
	if err != nil || !rep.OK || checked(rep) != ".: tsc" {
		t.Fatalf("clean check: %+v %v", rep, err)
	}

	// Changing a signature breaks a caller in another file: tsc checks the whole project
	// through the shadow root and reports the caller.
	cs := replaceIn(t, root, "src/service/book.ts", "async list(f: BookFilter): Promise<Book[]> {", "async list(f: BookFilter, page: number): Promise<Book[]> {")
	rep, err = g.Check(ctx, cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, d := range rep.Diagnostics {
		files = append(files, d.File)
	}
	if rep.OK || !strings.Contains(strings.Join(files, " "), "src/transport/http.ts") {
		t.Fatalf("report = %+v", rep)
	}

	// prettier formats and inserted imports land after the existing ones.
	cs = replaceIn(t, root, "src/transport/http.ts", "  return { status: 200, body: books };", "  return {status:200,body:books}")
	diags, err := g.Prepare(ctx, cs, map[string][]string{"src/transport/http.ts": {`import type { Book } from "../domain/book";`, "fs"}})
	if err != nil || len(diags) != 1 || diags[0].Severity != SeverityWarning {
		t.Fatalf("prepare: %v %v", diags, err)
	}
	src, _, _ := cs.Read("src/transport/http.ts")
	if !strings.Contains(string(src), "return { status: 200, body: books };") ||
		!strings.Contains(string(src), "import type { BookService } from \"../service/book\";\nimport type { Book } from \"../domain/book\";") {
		t.Errorf("prepared:\n%s", src)
	}
}

func TestTypeScriptTests(t *testing.T) {
	g, root := gateFor(t, "ts-layered", "tsc", "vitest")
	cs := replaceIn(t, root, "src/service/book.ts", "return books.length;", "return books.length + 1;")
	res, err := g.Test(context.Background(), cs, "src/service/book.ts", "counts")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ran || res.Passed || !strings.Contains(res.Command, "vitest run") {
		t.Fatalf("result = %+v", res)
	}
}

func TestTSXGate(t *testing.T) {
	g, root := gateFor(t, "tsx-components", "tsc")
	cs := replaceIn(t, root, "src/components/BookList.tsx", "<BookItem key={b.id} book={b} />", "<BookItem key={b.id} book={b.title} />")
	rep, err := g.Check(context.Background(), cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || rep.Diagnostics[0].File != "src/components/BookList.tsx" || rep.Diagnostics[0].Line != 17 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestMissingToolsOnlyForUsedLanguages(t *testing.T) {
	g, _ := gateFor(t, "py-layered")
	t.Setenv("PATH", t.TempDir())
	missing := strings.Join(g.MissingTools(), "\n")
	if !strings.Contains(missing, `"ruff"`) || strings.Contains(missing, "go build") || strings.Contains(missing, "tsc") || strings.Contains(missing, "pyright") {
		t.Fatalf("missing = %s", missing)
	}
}

func TestTypeScriptBiome(t *testing.T) {
	g, root := gateFor(t, "ts-layered", "tsc", "biome")
	tu.WriteFile(t, root, "biome.json", `{"linter": {"enabled": true, "rules": {"recommended": true}}}`)
	ctx := context.Background()

	// biome formats and organizes imports (fix group "format" picks biome over prettier).
	cs := replaceIn(t, root, "src/transport/http.ts", "  return { status: 200, body: books };", "  return {status:200,body:books}")
	if _, err := g.Prepare(ctx, cs, map[string][]string{"src/transport/http.ts": {`import type { Book } from "../domain/book";`}}); err != nil {
		t.Fatal(err)
	}
	src, _, _ := cs.Read("src/transport/http.ts")
	if !strings.Contains(string(src), "return { status: 200, body: books };") ||
		strings.Index(string(src), `"../domain/book"`) > strings.Index(string(src), `"../service/book"`) {
		t.Errorf("biome did not format/organize:\n%s", src)
	}

	cs = replaceIn(t, root, "src/service/book.ts", "    return books.length;", "    debugger;\n    return books.length;")
	rep, err := g.Check(ctx, cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if checked(rep) != ".: tsc;.: biome lint" || rep.OK {
		t.Fatalf("report = %+v", rep)
	}
	d := rep.Diagnostics[0]
	if d.File != "src/service/book.ts" || d.Source != "biome lint" || !strings.Contains(d.Message, "debugger") {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestJavaScriptESLint(t *testing.T) {
	g, root := gateFor(t, "ts-layered", "eslint")
	tu.WriteFile(t, root, "eslint.config.mjs", `export default [{ files: ["**/*.js"], rules: { "no-debugger": "error", "no-unused-vars": "warn" } }];`)
	tu.WriteFile(t, root, "src/util.js", "export function ok() {\n  return 1;\n}\n")
	tu.Git(t, root, "add", ".")
	tu.Git(t, root, "commit", "-qm", "js")
	cs := replaceIn(t, root, "src/util.js", "  return 1;", "  const unused = 2;\n  debugger;\n  return 1;")
	rep, err := g.Check(context.Background(), cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || len(rep.Diagnostics) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	sev := map[string]string{}
	for _, d := range rep.Diagnostics {
		sev[d.Message] = d.Severity
		if d.File != "src/util.js" || d.Source != "eslint" {
			t.Errorf("diagnostic = %+v", d)
		}
	}
	if sev["Unexpected 'debugger' statement. (no-debugger)"] != SeverityError {
		t.Errorf("severities = %v", sev)
	}
}

// TestRustGate runs only where cargo is installed (CI installs it).
func TestRustGate(t *testing.T) {
	g, root := gateFor(t, "rust-crate", "cargo", "rustfmt")
	ctx := context.Background()
	rep, err := g.Check(ctx, workspace.New(root), []string{"src"})
	if err != nil || !rep.OK {
		t.Fatalf("clean check: %+v %v", rep, err)
	}
	cs := replaceIn(t, root, "src/lib.rs", "self.title.is_empty()", "self.missing_field.is_empty()")
	rep, err = g.Check(ctx, cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || rep.Diagnostics[0].File != "src/lib.rs" || !strings.Contains(rep.Diagnostics[0].Message, "missing_field") {
		t.Fatalf("report = %+v", rep)
	}
	if strings.Contains(tu.ReadFile(t, root, "src/lib.rs"), "missing_field") {
		t.Fatal("real file changed")
	}
}

// TestCSharpGate runs only where the .NET SDK is installed.
func TestCSharpGate(t *testing.T) {
	g, root := gateFor(t, "csharp-lib", "dotnet")
	ctx := context.Background()
	cs := replaceIn(t, root, "Catalog.cs", "return _books.TryGetValue(id, out var b) ? b : null;", "return Missing(id);")
	rep, err := g.Check(ctx, cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || rep.Diagnostics[0].File != "Catalog.cs" || !strings.Contains(rep.Diagnostics[0].Message, "CS0103") {
		t.Fatalf("report = %+v", rep)
	}
}
