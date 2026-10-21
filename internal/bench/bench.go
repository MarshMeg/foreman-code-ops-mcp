// Package bench measures validator latency, prompt-cache hits and cost on a realistic
// review request (plan §7a).
package bench

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MarshMeg/foreman-code-ops/internal/pipeline"
	"github.com/MarshMeg/foreman-code-ops/internal/providers"
)

// Sample is a fixed review task: a small service method with context.
var Sample = pipeline.ReviewInput{
	Lang:  "go",
	Style: "## Errors\nWrap errors with fmt.Errorf(\"<operation>: %w\", err).\n\n## Go\nAccept context.Context first.",
	Context: []pipeline.Snippet{
		{Ref: "internal/repo/book.go:BookRepo", Source: "// BookRepo stores books.\ntype BookRepo interface {\n\tList(ctx context.Context, f domain.BookFilter) ([]domain.Book, error)\n}"},
		{Ref: "internal/domain/book.go:BookFilter", Source: "// BookFilter narrows List results.\ntype BookFilter struct {\n\tTitle  string\n\tLimit  int\n\tOffset int\n}"},
	},
	Task: pipeline.Task{File: "internal/service/book.go", Symbol: "BookService.Count", Kind: "method",
		Spec:      "Return the number of books in the repository. Wrap repository errors.",
		Interface: "func (s *BookService) Count(ctx context.Context) (int, error)"},
	Code: "// Count returns the number of books.\nfunc (s *BookService) Count(ctx context.Context) (int, error) {\n\tbooks, err := s.repo.List(ctx, domain.BookFilter{})\n\tif err != nil {\n\t\treturn 0, fmt.Errorf(\"count books: %w\", err)\n\t}\n\treturn len(books), nil\n}",
	Diff: "--- a/internal/service/book.go\n+++ b/internal/service/book.go\n@@ -30,3 +30,7 @@\n-\treturn 0, nil\n+\tbooks, err := s.repo.List(ctx, domain.BookFilter{})\n+\tif err != nil {\n+\t\treturn 0, fmt.Errorf(\"count books: %w\", err)\n+\t}\n+\treturn len(books), nil\n",
}

// Result summarizes one model at one parallelism level.
type Result struct {
	Model      string
	Parallel   int
	Requests   int
	Errors     int
	P50, P95   time.Duration
	CacheShare float64 // cached prompt tokens / prompt tokens
	CostPer100 float64 // USD, 0 when the provider does not report cost
	FirstError string
}

// Run sends n review requests with the given parallelism.
func Run(ctx context.Context, p providers.Provider, n, parallel int) Result {
	req, err := pipeline.ReviewRequest(Sample)
	if err != nil {
		return Result{Model: p.Name(), Errors: n, FirstError: err.Error()}
	}
	var mu sync.Mutex
	var lat []time.Duration
	var usage providers.Usage
	res := Result{Model: p.Name(), Parallel: parallel, Requests: n}
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			var v providers.Verdict
			_, u, err := providers.Structured(ctx, p, req, &v)
			d := time.Since(start)
			mu.Lock()
			defer mu.Unlock()
			usage.Add(u)
			if err != nil {
				res.Errors++
				if res.FirstError == "" {
					res.FirstError = err.Error()
				}
				return
			}
			lat = append(lat, d)
		}()
	}
	wg.Wait()
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	res.P50, res.P95 = percentile(lat, 0.50), percentile(lat, 0.95)
	if usage.PromptTokens > 0 {
		res.CacheShare = float64(usage.CachedTokens) / float64(usage.PromptTokens)
	}
	if ok := n - res.Errors; ok > 0 {
		res.CostPer100 = usage.Cost / float64(ok) * 100
	}
	return res
}

func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1)*q + 0.5)
	return sorted[i]
}

// WriteMarkdown prints results as a Markdown table.
func WriteMarkdown(w io.Writer, rs []Result) {
	fmt.Fprintln(w, "| Model | Parallel | OK/Total | p50 | p95 | Cache read | $ per 100 reviews |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|")
	for _, r := range rs {
		cost := "n/a"
		if r.CostPer100 > 0 {
			cost = fmt.Sprintf("%.4f", r.CostPer100)
		}
		fmt.Fprintf(w, "| %s | %d | %d/%d | %s | %s | %.0f%% | %s |\n", r.Model, r.Parallel, r.Requests-r.Errors, r.Requests,
			r.P50.Round(10*time.Millisecond), r.P95.Round(10*time.Millisecond), r.CacheShare*100, cost)
	}
	var errs []string
	for _, r := range rs {
		if r.FirstError != "" {
			errs = append(errs, fmt.Sprintf("- %s ×%d: %s", r.Model, r.Parallel, r.FirstError))
		}
	}
	if len(errs) > 0 {
		fmt.Fprintln(w, "\nErrors:\n"+strings.Join(errs, "\n"))
	}
}
