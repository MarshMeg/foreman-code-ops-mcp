package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/budget"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/gate"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/providers"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/workspace"
)

// validatorMaxTokens bounds validator answers (reviews and rewrites of one symbol).
const validatorMaxTokens = 16000

// Who wrote the accepted code.
const (
	WrittenByLocal     = "local"
	WrittenByValidator = "validator"
	WrittenByNone      = "none"
)

// Env holds everything the pipeline needs. Source/Exists describe the file when the task started.
type Env struct {
	Root      string
	Adapter   lang.Adapter
	Gate      *gate.Gate
	Generator providers.Provider
	// Acquire reserves a generator slot for one model call.
	Acquire       func(context.Context) (func(), error)
	Count         budget.Counter
	SlotCtx       int
	OutputReserve int
	// Validator reviews generated code and writes fallbacks; nil disables both.
	Validator providers.Provider
	// AcquireValidator limits concurrent validator requests.
	AcquireValidator func(context.Context) (func(), error)
	ValidatorTimeout time.Duration
	Limits           config.Limits
	Style            string
	Context          []Snippet
	Source           []byte
	Exists           bool
	Siblings         [][]byte
	Log              *slog.Logger
}

// Outcome is the result of Run.
type Outcome struct {
	// OK is true when the change passed the gate (and review); Changes then holds it.
	OK         bool
	Changes    *workspace.Changeset
	Code       string
	Imports    []string
	Symbol     lang.Symbol
	WrittenBy  string // local | validator | none
	ReviewedBy string // validator name when a review accepted the code
	BuildFixes int
	Reviews    int
	// FallbackAttempts counts gate attempts of validator-written code.
	FallbackAttempts int
	MinorNotes       []string
	Diagnostics      []gate.Diagnostic
	// ReviewIssues are the last unresolved major review findings.
	ReviewIssues []string
	// Dropped lists context parts that did not fit into the generator slot.
	Dropped        []string
	Warnings       []string
	Usage          providers.Usage
	ValidatorUsage providers.Usage
}

// run carries the state of one Run.
type run struct {
	env     Env
	t       Task
	base    []byte
	current lang.Symbol
	found   bool
	system  string
	out     Outcome
}

// Run writes and checks the symbol:
//
//	generate → gate → (fix on errors, up to max_build_fixes) → review → (fix on major
//	issues, up to max_reviews) → accept; when the generator runs out of attempts, the
//	validator writes the symbol (up to max_fallback_attempts gate runs).
func Run(ctx context.Context, env Env, t Task) (Outcome, error) {
	if env.Log == nil {
		env.Log = slog.New(slog.DiscardHandler)
	}
	r := &run{env: env, t: t, out: Outcome{WrittenBy: WrittenByNone}}
	var err error
	if r.base, err = baseSource(env, t); err != nil {
		return Outcome{}, err
	}
	if r.current, r.found, err = findSymbol(env.Adapter, r.base, t.Symbol); err != nil {
		return Outcome{}, err
	}
	if !r.found {
		// Fail on an impossible placement (missing anchor, class without members) before
		// spending a model call.
		if _, err := splice(env.Adapter, r.base, t, "placeholder"); err != nil {
			return Outcome{}, err
		}
	}
	if r.system, err = render("generate.md", map[string]any{"Lang": env.Adapter.Lang(), "Fragment": env.Adapter.PromptFragment()}); err != nil {
		return Outcome{}, err
	}
	if t.Direct {
		if env.Validator == nil {
			return Outcome{}, errors.New("mode=direct needs a validator, but validator.provider is none")
		}
		return r.fallback(ctx, nil, nil)
	}
	return r.local(ctx)
}

// local runs the generator loop.
func (r *run) local(ctx context.Context) (Outcome, error) {
	env := r.env
	prompt, dropped, err := buildPrompt(ctx, env, r.t, r.base, r.current, r.found, r.system, env.SlotCtx-env.OutputReserve, env.Count)
	if err != nil {
		return Outcome{}, err
	}
	r.out.Dropped = dropped

	msgs := []providers.Message{{Role: "user", Content: prompt}}
	for {
		gen, answer, err := r.call(ctx, env.Generator, env.Acquire, env.Limits.GenerateTimeout.D(), env.OutputReserve, r.system, msgs, &r.out.Usage)
		if errors.Is(err, providers.ErrInvalidAnswer) {
			r.out.Diagnostics = []gate.Diagnostic{{Severity: gate.SeverityError, Source: "generator", Message: err.Error()}}
			return r.fallback(ctx, []string{err.Error()}, nil)
		}
		if err != nil {
			return r.out, err
		}
		r.out.Code, r.out.Imports = gen.Code, gen.Imports

		res, err := Apply(ctx, env, r.t, r.base, gen)
		if err != nil {
			return r.out, err
		}
		var fix map[string]any
		if gate.HasErrors(res.Diagnostics) {
			r.out.Diagnostics = res.Diagnostics
			if r.out.BuildFixes >= env.Limits.MaxBuildFixes {
				return r.fallback(ctx, errorLines(res.Diagnostics), nil)
			}
			r.out.BuildFixes++
			env.Log.Info("gate failed, asking generator to fix", "symbol", r.t.Symbol, "round", r.out.BuildFixes, "errors", countErrors(res.Diagnostics))
			fix = map[string]any{"File": r.t.File, "Diagnostics": errorLines(res.Diagnostics), "Issues": nil, "Code": res.Region}
		} else {
			r.out.Diagnostics = res.Diagnostics
			if env.Validator == nil {
				return r.accept(res, gen, WrittenByLocal), nil
			}
			v, err := r.review(ctx, res)
			if err != nil {
				// A passing local result is kept when the reviewer is unreachable.
				r.out.Warnings = append(r.out.Warnings, "review skipped: "+err.Error())
				return r.accept(res, gen, WrittenByLocal), nil
			}
			if v.OK || v.Severity != providers.SeverityMajor {
				r.out.ReviewedBy = env.Validator.Name()
				r.out.MinorNotes = issueLines(v.Issues)
				return r.accept(res, gen, WrittenByLocal), nil
			}
			r.out.ReviewIssues = issueLines(v.Issues)
			if r.out.Reviews >= env.Limits.MaxReviews {
				return r.fallback(ctx, r.out.ReviewIssues, v.Rewrite)
			}
			r.out.Reviews++
			env.Log.Info("review found major issues, asking generator to fix", "symbol", r.t.Symbol, "round", r.out.Reviews)
			fix = map[string]any{"File": r.t.File, "Diagnostics": nil, "Issues": r.out.ReviewIssues, "Code": res.Region}
		}
		text, err := render("fix.md", fix)
		if err != nil {
			return r.out, err
		}
		// Only the latest attempt is sent back, so the prompt stays within the slot budget.
		msgs = []providers.Message{{Role: "user", Content: prompt}, {Role: "assistant", Content: answer}, {Role: "user", Content: text}}
	}
}

// fallback lets the validator write the symbol. problems describe why the generator's last
// attempt was not accepted; rewrite is a corrected version from the last review, tried first.
func (r *run) fallback(ctx context.Context, problems []string, rewrite *providers.GeneratedCode) (Outcome, error) {
	env := r.env
	if env.Validator == nil {
		return r.out, nil
	}
	// The validator's context window is large: give it everything, unbudgeted.
	prompt, _, err := buildPrompt(ctx, env, r.t, r.base, r.current, r.found, r.system, math.MaxInt32, estimate)
	if err != nil {
		return r.out, err
	}
	extra, err := render("fallback.md", map[string]any{"Code": r.out.Code, "Problems": problems})
	if err != nil {
		return r.out, err
	}
	user := prompt + "\n\n" + extra
	msgs := []providers.Message{{Role: "user", Content: user}}
	attempts := max(1, env.Limits.MaxFallbackAttempts)

	var gen providers.GeneratedCode
	var answer string
	if rewrite != nil && rewrite.Code != "" {
		gen = *rewrite
		b, _ := json.Marshal(gen)
		answer = string(b)
	}
	for i := 1; ; i++ {
		if answer == "" {
			gen, answer, err = r.call(ctx, env.Validator, env.AcquireValidator, env.ValidatorTimeout, validatorMaxTokens, r.system, msgs, &r.out.ValidatorUsage)
			if errors.Is(err, providers.ErrInvalidAnswer) {
				r.out.Diagnostics = []gate.Diagnostic{{Severity: gate.SeverityError, Source: "validator", Message: err.Error()}}
				return r.out, nil
			}
			if err != nil {
				return r.out, err
			}
		}
		r.out.FallbackAttempts = i
		r.out.Code, r.out.Imports = gen.Code, gen.Imports
		res, err := Apply(ctx, env, r.t, r.base, gen)
		if err != nil {
			return r.out, err
		}
		r.out.Diagnostics = res.Diagnostics
		if !gate.HasErrors(res.Diagnostics) {
			return r.accept(res, gen, WrittenByValidator), nil
		}
		if i >= attempts {
			return r.out, nil
		}
		env.Log.Info("validator code failed the gate", "symbol", r.t.Symbol, "attempt", i)
		text, err := render("fix.md", map[string]any{"File": r.t.File, "Diagnostics": errorLines(res.Diagnostics), "Issues": nil, "Code": res.Region})
		if err != nil {
			return r.out, err
		}
		msgs = []providers.Message{{Role: "user", Content: user}, {Role: "assistant", Content: answer}, {Role: "user", Content: text}}
		answer = ""
	}
}

func (r *run) accept(res Applied, gen providers.GeneratedCode, by string) Outcome {
	r.out.OK, r.out.Changes, r.out.Symbol = true, res.Changes, res.Symbol
	r.out.Code, r.out.Imports, r.out.WrittenBy = gen.Code, gen.Imports, by
	r.out.ReviewIssues = nil
	return r.out
}

// call makes one structured model call within a slot and a timeout, returning the decoded
// answer and its canonical JSON for fix rounds.
func (r *run) call(ctx context.Context, p providers.Provider, acquire func(context.Context) (func(), error), timeout time.Duration,
	maxTokens int, system string, msgs []providers.Message, usage *providers.Usage) (providers.GeneratedCode, string, error) {
	if acquire != nil {
		release, err := acquire(ctx)
		if err != nil {
			return providers.GeneratedCode{}, "", err
		}
		defer release()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var gen providers.GeneratedCode
	_, u, err := providers.Structured(ctx, p, providers.Request{
		System:     system,
		Messages:   msgs,
		Schema:     providers.GeneratorSchema(),
		SchemaName: "generated_code",
		MaxTokens:  maxTokens,
	}, &gen)
	usage.Add(u)
	if err != nil {
		return gen, "", err
	}
	answer, _ := json.Marshal(gen)
	return gen, string(answer), nil
}

func estimate(_ context.Context, text string) (int, error) {
	return providers.EstimateTokens(text), nil
}

func issueLines(issues []providers.Issue) []string {
	out := make([]string, 0, len(issues))
	for _, is := range issues {
		s := is.Problem
		if is.Line != nil && *is.Line > 0 {
			s = fmt.Sprintf("line %d: %s", *is.Line, s)
		}
		if is.Fix != nil && *is.Fix != "" {
			s += " (fix: " + *is.Fix + ")"
		}
		out = append(out, s)
	}
	return out
}
