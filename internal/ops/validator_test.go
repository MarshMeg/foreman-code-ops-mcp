package ops

import (
	"context"
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
	tu "github.com/MarshMeg/foreman-code-ops-mcp/internal/testutil"
)

var goodCount = tu.Answer{Code: countImpl, Imports: []string{`"fmt"`}}

// withValidator points the validator at a fake OpenAI-compatible server.
func withValidator(val *tu.FakeLLM) func(*config.Config) {
	return func(c *config.Config) {
		c.Validator.Provider, c.Validator.BaseURL, c.Validator.Model = "openai", val.URL, "val"
	}
}

func TestReviewAccepts(t *testing.T) {
	gen := tu.NewFakeLLM(t, goodCount)
	val := tu.NewFakeLLM(t)
	val.AddRaw(tu.Verdict(true, "minor", []string{"comment could mention errors"}, nil))
	o, dir := upsertOps(t, gen, withValidator(val))

	res, err := o.Upsert(context.Background(), countRequest())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusApplied || res.WrittenBy != "local" || res.ReviewedBy != "openai/val" || len(res.MinorNotes) != 1 {
		t.Fatalf("result = %+v", res)
	}
	msg := tu.Git(t, dir, "log", "-1", "--format=%B")
	if !strings.Contains(msg, "Foreman-Reviewed-By: openai/val") || !strings.Contains(msg, "Foreman-Attempts: build=0 review=0") {
		t.Fatalf("message:\n%s", msg)
	}
	p := val.Prompts()[0]
	for _, want := range []string{"You review one go symbol", "## Code to review", "## Diff of the file", "count books: %w", "## Context: internal/repo/book.go:BookRepo"} {
		if !strings.Contains(p, want) {
			t.Errorf("review prompt lacks %q", want)
		}
	}
}

func TestReviewMajorThenFixed(t *testing.T) {
	sloppy := tu.Answer{Code: "// Count returns the number of books.\nfunc (s *BookService) Count(ctx context.Context) (int, error) {\n\tbooks, _ := s.repo.List(ctx, domain.BookFilter{})\n\treturn len(books), nil\n}"}
	gen := tu.NewFakeLLM(t, sloppy, goodCount)
	val := tu.NewFakeLLM(t)
	val.AddRaw(tu.Verdict(false, "major", []string{"the error from List is ignored"}, nil))
	val.AddRaw(tu.Verdict(true, "none", nil, nil))
	o, dir := upsertOps(t, gen, withValidator(val))

	res, err := o.Upsert(context.Background(), countRequest())
	if err != nil || res.Status != StatusApplied || res.Reviews != 1 || res.ReviewedBy == "" {
		t.Fatalf("result = %+v %v", res, err)
	}
	if !strings.Contains(gen.Prompts()[1], "a reviewer found problems") || !strings.Contains(gen.Prompts()[1], "line 1: the error from List is ignored") {
		t.Errorf("fix prompt:\n%s", gen.Prompts()[1])
	}
	if !strings.Contains(tu.Git(t, dir, "log", "-1", "--format=%B"), "build=0 review=1") {
		t.Error("attempts trailer")
	}
}

func TestReviewExhaustedUsesRewrite(t *testing.T) {
	sloppy := tu.Answer{Code: "// Count returns the number of books.\nfunc (s *BookService) Count(ctx context.Context) (int, error) {\n\treturn 0, nil\n}"}
	gen := tu.NewFakeLLM(t, sloppy)
	val := tu.NewFakeLLM(t)
	val.AddRaw(tu.Verdict(false, "major", []string{"always returns zero"}, &goodCount))
	o, dir := upsertOps(t, gen, withValidator(val))

	zero := 0
	req := countRequest()
	req.Limits = &config.LimitsOverride{MaxReviews: &zero}
	res, err := o.Upsert(context.Background(), req)
	if err != nil || res.Status != StatusApplied || res.WrittenBy != "validator" {
		t.Fatalf("result = %+v %v", res, err)
	}
	if len(val.Prompts()) != 1 {
		t.Fatalf("validator calls = %d, the rewrite should be used without another call", len(val.Prompts()))
	}
	msg := tu.Git(t, dir, "log", "-1", "--format=%B")
	if !strings.Contains(msg, "Foreman-Written-By: validator (openai/val)") || !strings.Contains(msg, "fallback=1") {
		t.Fatalf("message:\n%s", msg)
	}
}

func TestBuildFailuresFallBackToValidator(t *testing.T) {
	broken := tu.Answer{Code: "func (s *BookService) Count(ctx context.Context) (int, error) { return nope, nil }"}
	gen := tu.NewFakeLLM(t, broken)
	val := tu.NewFakeLLM(t, goodCount)
	o, dir := upsertOps(t, gen, withValidator(val))

	zero := 0
	req := countRequest()
	req.Limits = &config.LimitsOverride{MaxBuildFixes: &zero}
	res, err := o.Upsert(context.Background(), req)
	if err != nil || res.Status != StatusApplied || res.WrittenBy != "validator" || res.ReviewedBy != "" {
		t.Fatalf("result = %+v %v", res, err)
	}
	p := val.Prompts()[0]
	if !strings.Contains(p, "A smaller model tried to write this symbol") || !strings.Contains(p, "return nope, nil") || !strings.Contains(p, "undefined: nope") {
		t.Errorf("fallback prompt:\n%s", p)
	}
	if !strings.Contains(tu.ReadFile(t, dir, "internal/service/book.go"), "count books: %w") {
		t.Fatal("validator code not committed")
	}
}

func TestFallbackFails(t *testing.T) {
	broken := tu.Answer{Code: "func (s *BookService) Count(ctx context.Context) (int, error) { return nope, nil }"}
	gen := tu.NewFakeLLM(t, broken)
	val := tu.NewFakeLLM(t, broken, broken)
	o, dir := upsertOps(t, gen, withValidator(val))
	head := tu.Git(t, dir, "rev-parse", "HEAD")

	zero := 0
	req := countRequest()
	req.Limits = &config.LimitsOverride{MaxBuildFixes: &zero}
	res, err := o.Upsert(context.Background(), req)
	if err != nil || res.Status != StatusFailed || res.WrittenBy != "none" || !strings.Contains(res.Message, "nor the validator (2 attempt(s))") {
		t.Fatalf("result = %+v %v", res, err)
	}
	if !strings.Contains(val.Prompts()[1], "the checks failed") {
		t.Errorf("second validator prompt:\n%s", val.Prompts()[1])
	}
	if tu.Git(t, dir, "rev-parse", "HEAD") != head {
		t.Fatal("failed upsert committed")
	}
}

func TestDirectMode(t *testing.T) {
	gen := tu.NewFakeLLM(t)
	val := tu.NewFakeLLM(t, goodCount)
	o, _ := upsertOps(t, gen, withValidator(val))
	req := countRequest()
	req.Mode = "direct"
	res, err := o.Upsert(context.Background(), req)
	if err != nil || res.Status != StatusApplied || res.WrittenBy != "validator" {
		t.Fatalf("result = %+v %v", res, err)
	}
	if len(gen.Prompts()) != 0 {
		t.Fatal("generator called in direct mode")
	}
	if strings.Contains(val.Prompts()[0], "A smaller model tried") {
		t.Error("direct prompt mentions a previous attempt")
	}
}

func TestDirectModeWithoutValidator(t *testing.T) {
	gen := tu.NewFakeLLM(t)
	o, _ := upsertOps(t, gen)
	req := countRequest()
	req.Mode = "direct"
	if _, err := o.Upsert(context.Background(), req); err == nil || !strings.Contains(err.Error(), "needs a validator") {
		t.Fatalf("err = %v", err)
	}
}

func TestReviewerDownKeepsLocalResult(t *testing.T) {
	gen := tu.NewFakeLLM(t, goodCount)
	val := tu.NewFakeLLM(t)
	val.Fail = true
	o, dir := upsertOps(t, gen, withValidator(val))
	res, err := o.Upsert(context.Background(), countRequest())
	if err != nil || res.Status != StatusApplied || res.ReviewedBy != "" || !strings.Contains(strings.Join(res.Warnings, " "), "review skipped") {
		t.Fatalf("result = %+v %v", res, err)
	}
	if strings.Contains(tu.Git(t, dir, "log", "-1", "--format=%B"), "Reviewed-By") {
		t.Fatal("unreviewed commit claims a review")
	}
}

func TestInvalidGeneratorAnswerFallsBack(t *testing.T) {
	gen := tu.NewFakeLLM(t)
	gen.AddRaw("I cannot do that")
	gen.AddRaw("still no JSON")
	val := tu.NewFakeLLM(t, goodCount)
	o, _ := upsertOps(t, gen, withValidator(val))
	res, err := o.Upsert(context.Background(), countRequest())
	if err != nil || res.Status != StatusApplied || res.WrittenBy != "validator" {
		t.Fatalf("result = %+v %v", res, err)
	}
}
