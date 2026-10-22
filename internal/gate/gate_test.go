package gate

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang/builtin"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/project"
	tu "github.com/MarshMeg/foreman-code-ops-mcp/internal/testutil"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/workspace"
)

const fixture = "../../testdata/go-layered"

func newGate(t *testing.T) (*Gate, string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH")
	}
	dir := tu.RepoFromFixture(t, fixture)
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

const serviceFile = "internal/service/book.go"

func replaceIn(t *testing.T, root, rel, old, new string) *workspace.Changeset {
	t.Helper()
	src := tu.ReadFile(t, root, rel)
	if !strings.Contains(src, old) {
		t.Fatalf("%s does not contain %q", rel, old)
	}
	cs := workspace.New(root)
	cs.Write(rel, []byte(strings.Replace(src, old, new, 1)))
	return cs
}

func TestCheckCleanProject(t *testing.T) {
	g, root := newGate(t)
	rep, err := g.Check(context.Background(), workspace.New(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || len(rep.Diagnostics) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if strings.Join(rep.Checked, ";") != ".: go build;.: go vet" {
		t.Fatalf("checked = %v", rep.Checked)
	}
}

func TestCheckCandidateCompileError(t *testing.T) {
	g, root := newGate(t)
	before := tu.ReadFile(t, root, serviceFile)
	cs := replaceIn(t, root, serviceFile, "return len(books), err", "return len(books), missingVar")

	rep, err := g.Check(context.Background(), cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || len(rep.Diagnostics) == 0 {
		t.Fatalf("report = %+v", rep)
	}
	var found bool
	for _, d := range rep.Diagnostics {
		if d.File == serviceFile && d.Line == 34 && d.Source == "go build" && strings.Contains(d.Message, "undefined: missingVar") {
			found = true
		}
		if strings.Contains(d.File, "foreman-gate-") {
			t.Errorf("overlay backing path leaked: %+v", d)
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v", rep.Diagnostics)
	}
	if strings.Join(rep.Checked, ";") != ".: go build" {
		t.Fatalf("vet should be skipped after build failure: %v", rep.Checked)
	}
	if tu.ReadFile(t, root, serviceFile) != before {
		t.Fatal("real file changed")
	}
}

func TestCheckVetFinding(t *testing.T) {
	g, root := newGate(t)
	cs := replaceIn(t, root, serviceFile, "func unusedHelper() {}", "func unusedHelper() { fmt.Printf(\"%d\", \"x\") }")
	if _, err := g.Prepare(context.Background(), cs, nil); err != nil { // goimports adds "fmt"
		t.Fatal(err)
	}
	rep, err := g.Check(context.Background(), cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || rep.Diagnostics[0].Source != "go vet" || rep.Diagnostics[0].File != serviceFile {
		t.Fatalf("report = %+v", rep)
	}
}

func TestCheckDeletedFile(t *testing.T) {
	g, root := newGate(t)
	cs := workspace.New(root)
	cs.Delete("internal/repo/book.go")
	rep, err := g.Check(context.Background(), cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatalf("expected failure without repo package, got %+v", rep)
	}
	var files []string
	for _, d := range rep.Diagnostics {
		files = append(files, d.File)
	}
	if !strings.Contains(strings.Join(files, " "), "internal/service/book.go") && !strings.Contains(rep.Diagnostics[0].Message, "repo") {
		t.Fatalf("diagnostics = %+v", rep.Diagnostics)
	}
}

func TestCheckPaths(t *testing.T) {
	g, root := newGate(t)
	rep, err := g.Check(context.Background(), workspace.New(root), []string{"internal/service", "internal/repo/book.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || strings.Join(rep.Checked, ";") != ".: go build;.: go vet" {
		t.Fatalf("report = %+v", rep)
	}
	gr, _, _ := g.groups(workspace.New(root), []string{"internal/service", "internal/repo/book.go"})
	if got := strings.Join(gr[0].packages(gr[0].adapter.Toolchain()), " "); got != "./internal/repo ./internal/service/..." {
		t.Fatalf("packages = %s", got)
	}
}

func TestPrepareFixesImportsAndFormat(t *testing.T) {
	g, root := newGate(t)
	cs := replaceIn(t, root, serviceFile, "func unusedHelper() {}",
		"func unusedHelper() string {\nreturn fmt.Sprint(strings.ToUpper(\"x\"))\n}")
	diags, err := g.Prepare(context.Background(), cs, map[string][]string{serviceFile: {`"strings"`, `errs "errors"`}})
	if err != nil || len(diags) != 0 {
		t.Fatalf("prepare: %v %v", diags, err)
	}
	src, _, _ := cs.Read(serviceFile)
	s := string(src)
	for _, want := range []string{"\t\"fmt\"\n", "\t\"strings\"\n", "\treturn fmt.Sprint(strings.ToUpper(\"x\"))\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, `"errors"`) {
		t.Errorf("unused import kept:\n%s", s)
	}
	if rep, _ := g.Check(context.Background(), cs, nil); !rep.OK {
		t.Fatalf("check after prepare: %+v", rep)
	}
}

func TestPrepareSyntaxError(t *testing.T) {
	g, root := newGate(t)
	cs := replaceIn(t, root, serviceFile, "func unusedHelper() {}", "func unusedHelper() {")
	diags, err := g.Prepare(context.Background(), cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !HasErrors(diags) || diags[0].File != serviceFile || diags[0].Line == 0 || diags[0].Source != "goimports" {
		t.Fatalf("diags = %+v", diags)
	}
}

func TestUnformatted(t *testing.T) {
	g, root := newGate(t)
	tu.WriteFile(t, root, "internal/domain/ugly.go", "package domain\nfunc  Ugly( ) {}\n")
	changed, diags, err := g.Unformatted(context.Background(), []string{"internal/domain/ugly.go", "internal/domain/book.go"})
	if err != nil || len(diags) != 0 {
		t.Fatal(err, diags)
	}
	if strings.Join(changed, ",") != "internal/domain/ugly.go" {
		t.Fatalf("changed = %v", changed)
	}
}

func TestRunTests(t *testing.T) {
	g, root := newGate(t)
	tu.WriteFile(t, root, "internal/service/book_test.go", `package service

import (
	"context"
	"testing"

	"example.com/books/internal/domain"
	"example.com/books/internal/repo"
)

func TestCount(t *testing.T) {
	s := NewBookService(repo.NewMemRepo(domain.Book{}, domain.Book{}))
	if n, _ := s.Count(context.Background()); n != 2 {
		t.Fatalf("n = %d", n)
	}
}

func TestOther(t *testing.T) {}
`)
	ctx := context.Background()

	res, err := g.Test(ctx, workspace.New(root), "internal/service", "TestCount")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ran || !res.Passed || !strings.Contains(res.Command, "-run TestCount") || !strings.Contains(res.Command, "./internal/service/...") {
		t.Fatalf("result = %+v", res)
	}

	// A candidate that breaks Count is seen by the tests through the overlay.
	cs := replaceIn(t, root, serviceFile, "return len(books), err", "return len(books) + 1, err")
	res, err = g.Test(ctx, cs, serviceFile, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed || !strings.Contains(res.Output, "n = 3") {
		t.Fatalf("result = %+v", res)
	}

	res, err = g.Test(ctx, workspace.New(root), "", "")
	if err != nil || !res.Passed || !strings.Contains(res.Command, "./...") {
		t.Fatalf("all: %+v %v", res, err)
	}
}

func TestMissingTool(t *testing.T) {
	g, root := newGate(t)
	t.Setenv("PATH", t.TempDir())
	rep, err := g.Check(context.Background(), workspace.New(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || !strings.Contains(rep.Diagnostics[0].Message, "not found in PATH") {
		t.Fatalf("report = %+v", rep)
	}
}
