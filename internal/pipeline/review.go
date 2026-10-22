package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/providers"
)

// ReviewInput is what the validator sees.
type ReviewInput struct {
	Lang    string
	Style   string
	Context []Snippet
	Task    Task
	Code    string // the symbol as it appears in the file
	Diff    string
}

// ReviewRequest builds the review request. Stable content (style guide, context) goes
// first so provider prompt caches can reuse it across reviews.
func ReviewRequest(in ReviewInput) (providers.Request, error) {
	system, err := render("review.md", map[string]any{"Lang": in.Lang})
	if err != nil {
		return providers.Request{}, err
	}
	var b strings.Builder
	if in.Style != "" {
		fmt.Fprintf(&b, "## Project style guide\n\n%s\n\n", in.Style)
	}
	for _, s := range in.Context {
		fmt.Fprintf(&b, "## Context: %s\n\n```\n%s\n```\n\n", s.Ref, s.Source)
	}
	t := in.Task
	fmt.Fprintf(&b, "## Task\n\nThe %s `%s` in `%s`.\n\nSpecification:\n%s\n\n", kindOr(t.Kind), t.Symbol, t.File, strings.TrimSpace(t.Spec))
	if strings.TrimSpace(t.Interface) != "" {
		fmt.Fprintf(&b, "Interface:\n```\n%s\n```\n\n", strings.TrimSpace(t.Interface))
	}
	fmt.Fprintf(&b, "## Code to review\n\n```\n%s\n```\n\n", in.Code)
	fmt.Fprintf(&b, "## Diff of the file\n\n```diff\n%s```\n", in.Diff)
	return providers.Request{
		System:     system,
		Messages:   []providers.Message{{Role: "user", Content: b.String()}},
		Schema:     providers.VerdictSchema(),
		SchemaName: "review_verdict",
		MaxTokens:  validatorMaxTokens,
	}, nil
}

// review asks the validator to judge code that passed the gate.
func (r *run) review(ctx context.Context, res Applied) (providers.Verdict, error) {
	env := r.env
	req, err := ReviewRequest(ReviewInput{Lang: env.Adapter.Lang(), Style: env.Style, Context: env.Context,
		Task: r.t, Code: res.Region, Diff: res.Changes.Diff()})
	if err != nil {
		return providers.Verdict{}, err
	}
	if env.AcquireValidator != nil {
		release, err := env.AcquireValidator(ctx)
		if err != nil {
			return providers.Verdict{}, err
		}
		defer release()
	}
	if env.ValidatorTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, env.ValidatorTimeout)
		defer cancel()
	}
	var v providers.Verdict
	_, u, err := providers.Structured(ctx, env.Validator, req, &v)
	r.out.ValidatorUsage.Add(u)
	return v, err
}
