package ops

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
	tu "github.com/MarshMeg/foreman-code-ops/internal/testutil"
)

const countImpl = `// Count returns the number of books.
func (s *BookService) Count(ctx context.Context) (int, error) {
	books, err := s.repo.List(ctx, domain.BookFilter{})
	if err != nil {
		return 0, fmt.Errorf("count books: %w", err)
	}
	return len(books), nil
}`

func upsertOps(t *testing.T, llm *tu.FakeLLM, mutate ...func(*config.Config)) (*Ops, string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH")
	}
	dir := tu.RepoFromFixture(t, fixture)
	return newOps(t, dir, append([]func(*config.Config){func(c *config.Config) {
		c.Generator.BaseURL = llm.URL
		c.Generator.Model = "test-model"
		c.Validator.Provider = "none"
	}}, mutate...)...), dir
}

func countRequest() UpsertRequest {
	return UpsertRequest{
		File: "internal/service/book.go", Symbol: "BookService.Count", Kind: "method",
		Spec:      "return the number of books from the repository; wrap errors",
		Interface: "func (s *BookService) Count(ctx context.Context) (int, error)",
		Context:   []Ref{{File: "internal/repo/book.go", Symbol: "BookRepo"}},
	}
}

func TestUpsertReplaceMethod(t *testing.T) {
	llm := tu.NewFakeLLM(t, tu.Answer{Code: countImpl, Imports: []string{`"fmt"`}})
	o, dir := upsertOps(t, llm)

	res, err := o.Upsert(context.Background(), countRequest())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusApplied || res.Commit == "" || res.WrittenBy != "local" || res.BuildFixes != 0 {
		t.Fatalf("result = %+v", res)
	}
	if res.Signature != "func (s *BookService) Count(ctx context.Context) (int, error)" {
		t.Errorf("signature = %q", res.Signature)
	}
	src := tu.ReadFile(t, dir, "internal/service/book.go")
	if !strings.Contains(src, `return 0, fmt.Errorf("count books: %w", err)`) || !strings.Contains(src, "\t\"fmt\"\n") {
		t.Errorf("file:\n%s", src)
	}
	msg := tu.Git(t, dir, "log", "-1", "--format=%B")
	for _, want := range []string{"foreman: upsert internal/service/book.go BookService.Count", "spec: return the number of books",
		"Foreman-Written-By: local (test-model)", "Foreman-Attempts: build=0 review=0"} {
		if !strings.Contains(msg, want) {
			t.Errorf("commit message lacks %q:\n%s", want, msg)
		}
	}
	// The prompt carried the task, the current code and the resolved context symbol.
	p := llm.Prompts()[0]
	for _, want := range []string{"## Task", "Current code, which your answer replaces entirely", "## Context: internal/repo/book.go:BookRepo",
		"List(ctx context.Context, f domain.BookFilter)", "## Other declarations in internal/service/book.go", "## Go"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

func TestUpsertNewMethodAfterAnchor(t *testing.T) {
	llm := tu.NewFakeLLM(t, tu.Answer{Code: `// Titles returns the titles of all books, upper-cased.
func (s *BookService) Titles(ctx context.Context) ([]string, error) {
	books, err := s.List(ctx, domain.BookFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(books))
	for _, b := range books {
		out = append(out, strings.ToUpper(b.Title))
	}
	return out, nil
}`})
	o, dir := upsertOps(t, llm)
	req := UpsertRequest{File: "internal/service/book.go", Symbol: "BookService.Titles", Kind: "method",
		Spec: "titles upper-cased", Interface: "func (s *BookService) Titles(ctx context.Context) ([]string, error)", Anchor: "BookService.List"}
	res, err := o.Upsert(context.Background(), req)
	if err != nil || res.Status != StatusApplied {
		t.Fatalf("result = %+v %v", res, err)
	}
	src := tu.ReadFile(t, dir, "internal/service/book.go")
	list, titles, count := strings.Index(src, ") List("), strings.Index(src, ") Titles("), strings.Index(src, ") Count(")
	if !(list < titles && titles < count) {
		t.Errorf("Titles not inserted after List:\n%s", src)
	}
	if !strings.Contains(src, "\t\"strings\"\n") {
		t.Errorf("goimports did not add strings:\n%s", src)
	}
}

func TestUpsertBuildFixLoop(t *testing.T) {
	broken := tu.Answer{Code: strings.Replace(countImpl, "return len(books), nil", "return len(bookz), nil", 1), Imports: []string{`"fmt"`}}
	llm := tu.NewFakeLLM(t, broken, tu.Answer{Code: countImpl})
	o, _ := upsertOps(t, llm)
	res, err := o.Upsert(context.Background(), countRequest())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusApplied || res.BuildFixes != 1 {
		t.Fatalf("result = %+v", res)
	}
	fix := llm.Prompts()[1]
	if !strings.Contains(fix, "undefined: bookz") || !strings.Contains(fix, "the checks failed") || !strings.Contains(fix, `"code"`) {
		t.Errorf("fix prompt:\n%s", fix)
	}
}

func TestUpsertGivesUp(t *testing.T) {
	broken := tu.Answer{Code: "func (s *BookService) Count(ctx context.Context) (int, error) { return nope, nil }"}
	llm := tu.NewFakeLLM(t, broken, broken)
	o, dir := upsertOps(t, llm)
	before := tu.ReadFile(t, dir, "internal/service/book.go")
	head := tu.Git(t, dir, "rev-parse", "HEAD")

	one := 1
	req := countRequest()
	req.Limits = &config.LimitsOverride{MaxBuildFixes: &one}
	res, err := o.Upsert(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFailed || res.WrittenBy != "none" || res.BuildFixes != 1 || res.LastCode == "" || len(res.Diagnostics) == 0 {
		t.Fatalf("result = %+v", res)
	}
	if tu.ReadFile(t, dir, "internal/service/book.go") != before || tu.Git(t, dir, "rev-parse", "HEAD") != head {
		t.Fatal("failed upsert changed the repository")
	}
}

func TestUpsertRejectsExtraSymbols(t *testing.T) {
	withHelper := tu.Answer{Code: countImpl + "\n\nfunc helper() {}", Imports: []string{`"fmt"`}}
	llm := tu.NewFakeLLM(t, withHelper, tu.Answer{Code: countImpl, Imports: []string{`"fmt"`}})
	o, dir := upsertOps(t, llm)
	res, err := o.Upsert(context.Background(), countRequest())
	if err != nil || res.Status != StatusApplied || res.BuildFixes != 1 {
		t.Fatalf("result = %+v %v", res, err)
	}
	if strings.Contains(tu.ReadFile(t, dir, "internal/service/book.go"), "helper") {
		t.Fatal("extra symbol committed")
	}
	if !strings.Contains(llm.Prompts()[1], "defines other symbols (helper)") {
		t.Errorf("fix prompt:\n%s", llm.Prompts()[1])
	}
}

func TestUpsertNewFile(t *testing.T) {
	llm := tu.NewFakeLLM(t, tu.Answer{Code: "// Author writes books.\ntype Author struct {\n\tName string\n}"})
	o, dir := upsertOps(t, llm)
	res, err := o.Upsert(context.Background(), UpsertRequest{File: "internal/domain/author.go", Symbol: "Author", Kind: "type",
		Spec: "an author with a name", Interface: "type Author struct{ Name string }"})
	if err != nil || res.Status != StatusApplied {
		t.Fatalf("result = %+v %v", res, err)
	}
	if got := tu.ReadFile(t, dir, "internal/domain/author.go"); got != "package domain\n\n// Author writes books.\ntype Author struct {\n\tName string\n}\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestUpsertContextTooLarge(t *testing.T) {
	llm := tu.NewFakeLLM(t)
	o, _ := upsertOps(t, llm, func(c *config.Config) {
		c.Generator.SlotCtx = 9000
		c.Generator.OutputReserve = 8900
	})
	llm.Slots, llm.SlotCtx = 3, 9000
	res, err := o.Upsert(context.Background(), countRequest())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFailed || !strings.Contains(res.Message, "context_too_large") {
		t.Fatalf("result = %+v", res)
	}
	if len(llm.Prompts()) != 0 {
		t.Fatal("generator called despite context_too_large")
	}
}

func TestUpsertDropsLowPriorityContext(t *testing.T) {
	llm := tu.NewFakeLLM(t, tu.Answer{Code: countImpl, Imports: []string{`"fmt"`}})
	// ~600 tokens for the prompt: the task and system prompt fit, the outline and imports do not.
	o, _ := upsertOps(t, llm, func(c *config.Config) {
		c.Generator.SlotCtx = 9000
		c.Generator.OutputReserve = 8400
	})
	llm.SlotCtx = 9000
	res, err := o.Upsert(context.Background(), countRequest())
	if err != nil || res.Status != StatusApplied {
		t.Fatalf("result = %+v %v", res, err)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "imports") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
	if strings.Contains(llm.Prompts()[0], "## Imports of") {
		t.Fatal("dropped part still in prompt")
	}
}

func TestUpsertRebasesOnConcurrentChange(t *testing.T) {
	llm := tu.NewFakeLLM(t, tu.Answer{Code: countImpl, Imports: []string{`"fmt"`}})
	o, dir := upsertOps(t, llm)
	// While the model "thinks", another change to the same file lands.
	llm.OnRequest = func(int) {
		src := tu.ReadFile(t, dir, "internal/service/book.go")
		tu.WriteFile(t, dir, "internal/service/book.go", strings.Replace(src, "func unusedHelper() {}", "func unusedHelper() { _ = 1 }", 1))
		tu.Git(t, dir, "commit", "-qam", "concurrent")
	}
	res, err := o.Upsert(context.Background(), countRequest())
	if err != nil || res.Status != StatusApplied {
		t.Fatalf("result = %+v %v", res, err)
	}
	src := tu.ReadFile(t, dir, "internal/service/book.go")
	if !strings.Contains(src, "_ = 1") || !strings.Contains(src, "count books: %w") {
		t.Fatalf("concurrent change lost:\n%s", src)
	}
}

func TestUpsertRefusesDirtyBeforeGenerating(t *testing.T) {
	llm := tu.NewFakeLLM(t)
	o, dir := upsertOps(t, llm)
	tu.WriteFile(t, dir, "internal/service/book.go", tu.ReadFile(t, dir, "internal/service/book.go")+"// wip\n")
	res, err := o.Upsert(context.Background(), countRequest())
	if err != nil || res.Status != StatusRefused {
		t.Fatalf("result = %+v %v", res, err)
	}
	if len(llm.Prompts()) != 0 {
		t.Fatal("generator called for a dirty file")
	}
}

func TestUpsertRunsTests(t *testing.T) {
	llm := tu.NewFakeLLM(t, tu.Answer{Code: countImpl, Imports: []string{`"fmt"`}})
	o, dir := upsertOps(t, llm)
	tu.WriteFile(t, dir, "internal/service/book_test.go", `package service

import (
	"context"
	"testing"

	"example.com/books/internal/domain"
	"example.com/books/internal/repo"
)

func TestCount(t *testing.T) {
	n, err := NewBookService(repo.NewMemRepo(domain.Book{})).Count(context.Background())
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
`)
	tu.Git(t, dir, "add", ".")
	tu.Git(t, dir, "commit", "-qm", "tests")
	req := countRequest()
	req.RunTests, req.TestFilter = true, "TestCount"
	res, err := o.Upsert(context.Background(), req)
	if err != nil || res.Status != StatusApplied || res.Tests == nil || !res.Tests.Passed {
		t.Fatalf("result = %+v %v", res, err)
	}
}

func TestUpsertValidation(t *testing.T) {
	llm := tu.NewFakeLLM(t)
	o, _ := upsertOps(t, llm)
	for name, mutate := range map[string]func(*UpsertRequest){
		"kind":    func(r *UpsertRequest) { r.Kind = "macro" },
		"direct":  func(r *UpsertRequest) { r.Mode = "direct" },
		"spec":    func(r *UpsertRequest) { r.Spec = " " },
		"context": func(r *UpsertRequest) { r.Context = []Ref{{File: "internal/repo/book.go", Symbol: "Nope"}} },
		"anchor":  func(r *UpsertRequest) { r.Symbol, r.Anchor = "BookService.New", "Nope" },
	} {
		req := countRequest()
		mutate(&req)
		if _, err := o.Upsert(context.Background(), req); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestUpsertParallel(t *testing.T) {
	llm := tu.NewFakeLLM(t)
	llm.Respond = func(prompt string) tu.Answer {
		switch {
		case strings.Contains(prompt, "`BookService.Count`"):
			return tu.Answer{Code: countImpl, Imports: []string{`"fmt"`}}
		case strings.Contains(prompt, "`Author`"):
			return tu.Answer{Code: "// Author writes books.\ntype Author struct{ Name string }"}
		default:
			return tu.Answer{Code: "// Lent reports whether the book is lent.\nfunc (b Book) Lent() bool { return b.Status == Lent }"}
		}
	}
	o, dir := upsertOps(t, llm)
	reqs := []UpsertRequest{
		countRequest(),
		{File: "internal/domain/author.go", Symbol: "Author", Kind: "type", Spec: "author", Interface: "type Author struct{ Name string }"},
		{File: "internal/domain/book.go", Symbol: "Book.Lent", Kind: "method", Spec: "lent?", Interface: "func (b Book) Lent() bool"},
	}
	var wg sync.WaitGroup
	results := make([]UpsertResult, len(reqs))
	errs := make([]error, len(reqs))
	for i, r := range reqs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = o.Upsert(context.Background(), r)
		}()
	}
	wg.Wait()
	for i := range reqs {
		if errs[i] != nil || results[i].Status != StatusApplied {
			t.Errorf("%s: %+v %v", reqs[i].Symbol, results[i], errs[i])
		}
	}
	if n := strings.Count(tu.Git(t, dir, "log", "--format=%s"), "foreman: upsert"); n != 3 {
		t.Fatalf("commits = %d", n)
	}
	if out := tu.Git(t, dir, "status", "--porcelain"); out != "" {
		t.Fatalf("tree not clean:\n%s", out)
	}
}
