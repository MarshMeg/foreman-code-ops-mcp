package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/journal"
	"github.com/MarshMeg/foreman-code-ops/internal/lang/builtin"
	"github.com/MarshMeg/foreman-code-ops/internal/ops"
	"github.com/MarshMeg/foreman-code-ops/internal/project"
	tu "github.com/MarshMeg/foreman-code-ops/internal/testutil"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectProject(t *testing.T, dir string, mutate ...func(*config.Config)) *sdk.ClientSession {
	t.Helper()
	return connectProjectWith(t, dir, nil, mutate...)
}

func connectProjectWith(t *testing.T, dir string, j *journal.Journal, mutate ...func(*config.Config)) *sdk.ClientSession {
	t.Helper()
	proj, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := builtin.Registry()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	for _, m := range mutate {
		m(&cfg)
	}
	o, err := ops.New(proj, reg, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return connect(t, Deps{ProjectDir: proj.Root, Config: cfg, Ops: o, Journal: j})
}

func TestE2ESymbolAndFileTools(t *testing.T) {
	dir := tu.RepoFromFixture(t, "../../testdata/go-layered")
	cs := connectProject(t, dir)

	got := decode[GetSymbolOutput](t, call(t, cs, "get_symbol", map[string]any{"file": "internal/repo/book.go", "symbol": "memRepo.List"}))
	if got.Kind != "method" || !strings.HasPrefix(got.Signature, "func (r *memRepo) List(") || got.StartLine != 31 {
		t.Fatalf("get_symbol = %+v", got)
	}

	res := call(t, cs, "get_symbol", map[string]any{"file": "internal/repo/book.go", "symbol": "Repo.List"})
	if !res.IsError || !strings.Contains(res.Content[0].(*sdk.TextContent).Text, "similar: memRepo.List") {
		t.Fatalf("expected not-found with candidates, got %+v", res.Content)
	}

	del := decode[ChangeOutput](t, call(t, cs, "delete_symbol", map[string]any{"file": "internal/service/book.go", "symbol": "BookService"}))
	if del.Status != "refused" || len(del.References) == 0 {
		t.Fatalf("delete_symbol = %+v", del)
	}

	del = decode[ChangeOutput](t, call(t, cs, "delete_symbol", map[string]any{"file": "internal/service/book.go", "symbol": "unusedHelper"}))
	if del.Status != "applied" || del.Commit == "" || !strings.Contains(del.Diff, "-func unusedHelper") {
		t.Fatalf("delete_symbol = %+v", del)
	}

	cr := decode[ChangeOutput](t, call(t, cs, "create_file", map[string]any{"path": "internal/repo/author.go"}))
	if cr.Status != "applied" || !strings.Contains(cr.Diff, "+package repo") {
		t.Fatalf("create_file = %+v", cr)
	}
	df := decode[ChangeOutput](t, call(t, cs, "delete_file", map[string]any{"path": "internal/repo/author.go"}))
	if df.Status != "applied" {
		t.Fatalf("delete_file = %+v", df)
	}
	if n := strings.Count(tu.Git(t, dir, "log", "--format=%s"), "foreman: "); n != 3 {
		t.Fatalf("foreman commits = %d", n)
	}
}

func TestE2ECheckAndRunTests(t *testing.T) {
	dir := tu.RepoFromFixture(t, "../../testdata/go-layered")
	tu.WriteFile(t, dir, "internal/domain/book_test.go", "package domain\n\nimport \"testing\"\n\nfunc TestFails(t *testing.T) { t.Fatal(\"boom\") }\n")
	cs := connectProject(t, dir)

	ch := decode[CheckOutput](t, call(t, cs, "check", map[string]any{}))
	if !ch.OK || len(ch.Checked) != 2 {
		t.Fatalf("check = %+v", ch)
	}
	tr := decode[TestResult](t, call(t, cs, "run_tests", map[string]any{"target": "internal/domain"}))
	if !tr.Ran || tr.Passed || !strings.Contains(tr.Output, "boom") {
		t.Fatalf("run_tests = %+v", tr)
	}
}

func TestE2EUpsertSymbol(t *testing.T) {
	dir := tu.RepoFromFixture(t, "../../testdata/go-layered")
	llm := tu.NewFakeLLM(t, tu.Answer{Code: "// Count returns the number of books.\nfunc (s *BookService) Count(ctx context.Context) (int, error) {\n\treturn 0, nil\n}"})
	llm.Slots = 2
	var jbuf bytes.Buffer
	cs := connectProjectWith(t, dir, journal.New(&jbuf), func(c *config.Config) {
		c.Generator.BaseURL, c.Generator.Model, c.Validator.Provider = llm.URL, "m", "none"
	})

	out := decode[UpsertSymbolOutput](t, call(t, cs, "upsert_symbol", map[string]any{
		"file": "internal/service/book.go", "symbol": "BookService.Count", "kind": "method",
		"spec": "always zero", "interface": "func (s *BookService) Count(ctx context.Context) (int, error)",
		"context":   []map[string]any{{"file": "internal/domain/book.go", "symbol": "Book"}},
		"run_tests": "TestNothing",
		"limits":    map[string]any{"max_build_fixes": 1, "check_timeout": "60s"},
	}))
	if out.Status != "applied" || out.Commit == "" || out.WrittenBy != "local" || !out.Tests.Ran || !strings.Contains(out.Diff, "+\treturn 0, nil") {
		t.Fatalf("upsert = %+v", out)
	}
	var e journal.Entry
	if err := json.Unmarshal([]byte(strings.SplitN(jbuf.String(), "\n", 2)[0]), &e); err != nil {
		t.Fatal(err)
	}
	if e.Tool != "upsert_symbol" || e.Lang != "go" || e.Mode != "local" || e.Commit != out.Commit ||
		e.Generator == nil || e.Generator.Model != "openai/m" || e.Generator.PromptTokens != 100 || e.Validator != nil {
		t.Fatalf("journal entry %+v (generator %+v)", e, e.Generator)
	}

	st := decode[StatusOutput](t, call(t, cs, "status", map[string]any{}))
	if st.Generator.Slots != 2 || !strings.Contains(strings.Join(st.Warnings, "\n"), "llama-server has 2") {
		t.Fatalf("status = %+v", st)
	}
}
