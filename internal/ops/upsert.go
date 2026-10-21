package ops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/MarshMeg/foreman-code-ops/internal/budget"
	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/gate"
	"github.com/MarshMeg/foreman-code-ops/internal/gitops"
	"github.com/MarshMeg/foreman-code-ops/internal/lang"
	"github.com/MarshMeg/foreman-code-ops/internal/pipeline"
	"github.com/MarshMeg/foreman-code-ops/internal/providers"
	"github.com/MarshMeg/foreman-code-ops/internal/style"
	"github.com/MarshMeg/foreman-code-ops/internal/workspace"
)

// Kinds accepted by upsert_symbol.
var Kinds = []string{"function", "method", "type", "class", "component", "interface"}

// Ref names a symbol in a file.
type Ref struct{ File, Symbol string }

// UpsertRequest is the input of Upsert.
type UpsertRequest struct {
	File, Symbol, Kind, Spec, Interface string
	Context                             []Ref
	Anchor, Mode, Package               string
	// RunTests runs the tests of the file's package after the gate passes; TestFilter narrows them.
	RunTests      bool
	TestFilter    string
	CommitMessage string
	Limits        *config.LimitsOverride
}

// UpsertResult is the outcome of Upsert.
type UpsertResult struct {
	Status      string // applied | failed | refused
	Message     string
	Symbol      string
	Signature   string
	Diff        string
	Commit      string
	WrittenBy   string // local | none
	BuildFixes  int
	Reviews     int
	Diagnostics []gate.Diagnostic
	Tests       *gate.TestResult
	LastCode    string
	Warnings    []string
}

// Upsert writes a symbol with the generator, checks it with the gate and commits it.
// Generation runs without holding the project lock, so independent symbols are written in
// parallel up to the generator's slots.
func (o *Ops) Upsert(ctx context.Context, req UpsertRequest) (UpsertResult, error) {
	if !slices.Contains(Kinds, req.Kind) {
		return UpsertResult{}, fmt.Errorf("kind %q is not one of %v", req.Kind, Kinds)
	}
	if req.Mode != "" && req.Mode != "local" {
		if req.Mode == "direct" {
			return UpsertResult{}, errors.New("mode=direct needs the validator, which arrives in the next stage; use mode=local")
		}
		return UpsertResult{}, fmt.Errorf("mode %q is not local or direct", req.Mode)
	}
	if strings.TrimSpace(req.Spec) == "" {
		return UpsertResult{}, errors.New("spec is required")
	}
	if o.gen.p == nil {
		return UpsertResult{}, o.gen.err
	}
	rel, abs, err := o.proj.Resolve(req.File)
	if err != nil {
		return UpsertResult{}, err
	}
	a, err := o.adapter(rel)
	if err != nil {
		return UpsertResult{}, err
	}
	src, err := os.ReadFile(abs)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return UpsertResult{}, err
	}

	// Refuse early instead of after an expensive generation.
	if res, refused, err := o.refuseDirty(ctx, rel); err != nil || refused {
		return res, err
	}

	snippets, err := o.resolveContext(req.Context)
	if err != nil {
		return UpsertResult{}, err
	}
	guide, err := style.Read(o.proj.Root)
	if err != nil {
		return UpsertResult{}, err
	}
	limits := o.cfg.Limits.With(req.Limits)
	o.gen.reconcile(ctx)

	env := pipeline.Env{
		Root:          o.proj.Root,
		Adapter:       a,
		Gate:          o.gate.WithLimits(limits),
		Generator:     o.gen.p,
		Acquire:       o.gen.sched.Acquire,
		Count:         o.gen.counter(),
		SlotCtx:       o.gen.effectiveSlotCtx(),
		OutputReserve: o.cfg.Generator.OutputReserve,
		Limits:        limits,
		Style:         style.Select(guide, a.Lang()),
		Context:       snippets,
		Source:        src,
		Exists:        exists,
		Siblings:      siblings(abs),
		Log:           o.log,
	}
	task := pipeline.Task{File: rel, Symbol: req.Symbol, Kind: req.Kind, Spec: req.Spec,
		Interface: req.Interface, Anchor: req.Anchor, Package: req.Package}

	out, err := pipeline.Run(ctx, env, task)
	if errors.Is(err, budget.ErrContextTooLarge) {
		return UpsertResult{Status: StatusFailed, Symbol: req.Symbol, WrittenBy: "none", Message: err.Error()}, nil
	}
	if err != nil {
		return UpsertResult{}, err
	}
	res := UpsertResult{
		Symbol:      req.Symbol,
		BuildFixes:  out.BuildFixes,
		Diagnostics: out.Diagnostics,
		WrittenBy:   "local",
	}
	if len(out.Dropped) > 0 {
		res.Warnings = append(res.Warnings, "context dropped to fit the generator slot: "+strings.Join(out.Dropped, ", "))
	}
	if !out.OK {
		res.Status, res.WrittenBy, res.LastCode = StatusFailed, "none", out.Code
		res.Message = fmt.Sprintf("the generated code did not pass the gate after %d fix round(s); refine the spec/interface/context or write it with mode=direct", out.BuildFixes)
		return res, nil
	}

	if req.RunTests {
		tr, err := env.Gate.Test(ctx, out.Changes, rel, req.TestFilter)
		if err != nil {
			res.Warnings = append(res.Warnings, "tests not run: "+err.Error())
		} else {
			res.Tests = &tr
		}
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	changes, applied, err := o.rebase(ctx, env, task, out, abs)
	if err != nil {
		return UpsertResult{}, err
	}
	if applied != nil && gate.HasErrors(applied.Diagnostics) {
		res.Status, res.WrittenBy, res.LastCode, res.Diagnostics = StatusFailed, "none", out.Code, applied.Diagnostics
		res.Message = "the file changed while the code was generated, and the code no longer passes the gate on the new version"
		return res, nil
	}
	sym := out.Symbol
	if applied != nil {
		sym = applied.Symbol
	}

	subject := req.CommitMessage
	if subject == "" {
		subject = fmt.Sprintf("upsert %s %s", rel, req.Symbol)
	}
	msg := gitops.Message{
		Subject: subject,
		Body:    "spec: " + strings.TrimSpace(req.Spec),
		Trailers: []gitops.Trailer{
			{Key: gitops.TrailerWrittenBy, Value: fmt.Sprintf("local (%s)", o.gen.p.Model())},
			{Key: gitops.TrailerAttempts, Value: fmt.Sprintf("build=%d review=0", out.BuildFixes)},
		},
	}
	cr, err := o.commit(ctx, changes, msg)
	if err != nil {
		return UpsertResult{}, err
	}
	res.Status, res.Message, res.Diff, res.Commit = cr.Status, cr.Message, cr.Diff, cr.Commit
	res.Warnings = append(res.Warnings, cr.Warnings...)
	res.Signature = sym.Signature
	if cr.Status == StatusUnchanged {
		res.Status = StatusApplied
		res.Message = "the generated code is identical to the current code; nothing committed"
	}
	return res, nil
}

// rebase re-applies the generated code when the file changed during generation. The caller
// holds o.mu. applied is nil when the file was unchanged.
func (o *Ops) rebase(ctx context.Context, env pipeline.Env, task pipeline.Task, out pipeline.Outcome, abs string) (*workspace.Changeset, *pipeline.Applied, error) {
	now, rerr := os.ReadFile(abs)
	nowExists := rerr == nil
	if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return nil, nil, rerr
	}
	if nowExists == env.Exists && bytes.Equal(now, env.Source) {
		return out.Changes, nil, nil
	}
	base := now
	if !nowExists {
		var err error
		if base, err = env.Adapter.NewFile(task.File, lang.NewFileOpts{Package: task.Package, Siblings: env.Siblings}); err != nil {
			return nil, nil, err
		}
	}
	res, err := pipeline.Apply(ctx, env, task, base, providers.GeneratedCode{Code: out.Code, Imports: out.Imports})
	if err != nil {
		return nil, nil, err
	}
	return res.Changes, &res, nil
}

// refuseDirty returns a refusal when rel has uncommitted user changes and git.dirty is refuse.
func (o *Ops) refuseDirty(ctx context.Context, rel string) (UpsertResult, bool, error) {
	if o.cfg.Git.Dirty == "include" {
		return UpsertResult{}, false, nil
	}
	repo, err := o.gitRepo(ctx)
	if err != nil {
		return UpsertResult{}, false, err
	}
	dirty, err := repo.Dirty(ctx, []string{rel})
	if err != nil || len(dirty) == 0 {
		return UpsertResult{}, false, err
	}
	return UpsertResult{Status: StatusRefused, WrittenBy: "none",
		Message: "uncommitted user changes in " + rel + "; commit or stash them, or set git.dirty: include"}, true, nil
}

func (o *Ops) resolveContext(refs []Ref) ([]pipeline.Snippet, error) {
	var out []pipeline.Snippet
	var errs []error
	for _, r := range refs {
		info, err := o.GetSymbol(r.File, r.Symbol)
		if err != nil {
			errs = append(errs, fmt.Errorf("context %s:%s: %w", r.File, r.Symbol, err))
			continue
		}
		out = append(out, pipeline.Snippet{Ref: info.File + ":" + info.Symbol.Name, Source: info.Source})
	}
	return out, errors.Join(errs...)
}
