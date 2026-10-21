// Package pipeline produces a checked change for one symbol: it builds the generator
// prompt within the slot budget, generates code, splices it into the file, runs the gate
// and loops on build/lint errors. It does not touch the real file or git; the caller
// commits the returned changeset.
package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/MarshMeg/foreman-code-ops/internal/budget"
	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/edit"
	"github.com/MarshMeg/foreman-code-ops/internal/gate"
	"github.com/MarshMeg/foreman-code-ops/internal/lang"
	"github.com/MarshMeg/foreman-code-ops/internal/providers"
	"github.com/MarshMeg/foreman-code-ops/internal/workspace"
	"github.com/MarshMeg/foreman-code-ops/prompts"
)

// maxFixDiagnostics caps the diagnostics shown to the generator in a fix round.
const maxFixDiagnostics = 20

// Task is what to write.
type Task struct {
	File      string // project-relative
	Symbol    string
	Kind      string
	Spec      string
	Interface string
	Anchor    string
	Package   string
}

// Snippet is resolved context: a symbol's source with its reference label.
type Snippet struct {
	Ref    string // "file:Symbol"
	Source string
}

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
	Limits        config.Limits
	Style         string
	Context       []Snippet
	Source        []byte
	Exists        bool
	Siblings      [][]byte
	Log           *slog.Logger
}

// Outcome is the result of Run.
type Outcome struct {
	// OK is true when the change passed the gate; Changes then holds it.
	OK          bool
	Changes     *workspace.Changeset
	Code        string
	Imports     []string
	Symbol      lang.Symbol
	BuildFixes  int
	Diagnostics []gate.Diagnostic
	// Dropped lists context parts that did not fit into the slot.
	Dropped []string
	Usage   providers.Usage
}

// Run generates and checks the symbol, retrying on gate errors up to Limits.MaxBuildFixes.
func Run(ctx context.Context, env Env, t Task) (Outcome, error) {
	if env.Log == nil {
		env.Log = slog.New(slog.DiscardHandler)
	}
	base, err := baseSource(env, t)
	if err != nil {
		return Outcome{}, err
	}
	current, found, err := findSymbol(env.Adapter, base, t.Symbol)
	if err != nil {
		return Outcome{}, err
	}
	if !found && t.Anchor != "" {
		if _, err := env.Adapter.Find(base, t.Anchor); err != nil {
			return Outcome{}, fmt.Errorf("anchor: %w", err)
		}
	}

	system, err := render("generate.md", map[string]any{"Lang": env.Adapter.Lang(), "Fragment": env.Adapter.PromptFragment()})
	if err != nil {
		return Outcome{}, err
	}
	prompt, dropped, err := buildPrompt(ctx, env, t, base, current, found, system)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Dropped: dropped}

	msgs := []providers.Message{{Role: "user", Content: prompt}}
	gen, answer, err := generate(ctx, env, system, msgs, &out.Usage)
	for {
		if errors.Is(err, providers.ErrInvalidAnswer) {
			out.Diagnostics = []gate.Diagnostic{{Severity: gate.SeverityError, Source: "generator", Message: err.Error()}}
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out.Code, out.Imports = gen.Code, gen.Imports

		res, err := Apply(ctx, env, t, base, gen)
		if err != nil {
			return out, err
		}
		if !gate.HasErrors(res.Diagnostics) {
			out.OK, out.Changes, out.Symbol, out.Diagnostics = true, res.Changes, res.Symbol, res.Diagnostics
			return out, nil
		}
		out.Diagnostics = res.Diagnostics
		if out.BuildFixes >= env.Limits.MaxBuildFixes {
			return out, nil
		}
		out.BuildFixes++
		env.Log.Info("gate failed, asking generator to fix", "symbol", t.Symbol, "round", out.BuildFixes, "errors", countErrors(res.Diagnostics))

		fix, err := render("fix.md", map[string]any{"File": t.File, "Diagnostics": errorLines(res.Diagnostics), "Code": res.Region})
		if err != nil {
			return out, err
		}
		// Only the latest attempt is sent back, so the prompt stays within the slot budget.
		msgs = []providers.Message{{Role: "user", Content: prompt}, {Role: "assistant", Content: answer}, {Role: "user", Content: fix}}
		gen, answer, err = generate(ctx, env, system, msgs, &out.Usage)
	}
}

// Applied is a candidate change after splicing, import fixing, formatting and checks.
type Applied struct {
	Changes     *workspace.Changeset
	Symbol      lang.Symbol
	Region      string // the symbol's text in the final file, when it could be located
	Diagnostics []gate.Diagnostic
}

// Apply splices generated code into base (the file content, or the new-file template),
// runs the gate's fixers and checks, and verifies that exactly the requested symbol was written.
func Apply(ctx context.Context, env Env, t Task, base []byte, gen providers.GeneratedCode) (Applied, error) {
	a := env.Adapter
	code := a.Sanitize(gen.Code)
	if strings.TrimSpace(code) == "" {
		return Applied{Diagnostics: []gate.Diagnostic{{Severity: gate.SeverityError, Source: "foreman", Message: "the generated code is empty"}}}, nil
	}
	before, err := a.Symbols(base)
	if err != nil {
		return Applied{}, err
	}
	src, err := splice(a, base, t, code)
	if err != nil {
		return Applied{}, err
	}
	cs := workspace.New(env.Root)
	if err := cs.Write(t.File, src); err != nil {
		return Applied{}, err
	}
	diags, err := env.Gate.Prepare(ctx, cs, map[string][]string{t.File: gen.Imports})
	if err != nil {
		return Applied{}, err
	}
	res := Applied{Changes: cs, Diagnostics: diags}
	if gate.HasErrors(diags) {
		return res, nil
	}

	final, _, err := cs.Read(t.File)
	if err != nil {
		return Applied{}, err
	}
	after, err := a.Symbols(final)
	if err != nil {
		return Applied{}, err
	}
	if sym, err := a.Find(final, t.Symbol); err != nil {
		res.Diagnostics = append(res.Diagnostics, gate.Diagnostic{File: t.File, Severity: gate.SeverityError, Source: "foreman",
			Message: fmt.Sprintf("the code does not define %s (%v); write exactly that symbol", t.Symbol, err)})
		return res, nil
	} else {
		res.Symbol, res.Region = sym, string(final[sym.Start:sym.End])
	}
	if extra := extraSymbols(before, after, t.Symbol); len(extra) > 0 {
		res.Diagnostics = append(res.Diagnostics, gate.Diagnostic{File: t.File, Severity: gate.SeverityError, Source: "foreman",
			Message: fmt.Sprintf("the code defines other symbols (%s); write only %s and use existing code", strings.Join(extra, ", "), t.Symbol)})
		return res, nil
	}

	rep, err := env.Gate.Check(ctx, cs, nil)
	if err != nil {
		return Applied{}, err
	}
	res.Diagnostics = append(res.Diagnostics, rep.Diagnostics...)
	return res, nil
}

func baseSource(env Env, t Task) ([]byte, error) {
	if env.Exists {
		return env.Source, nil
	}
	return env.Adapter.NewFile(t.File, lang.NewFileOpts{Package: t.Package, Siblings: env.Siblings})
}

func findSymbol(a lang.Adapter, src []byte, name string) (lang.Symbol, bool, error) {
	s, err := a.Find(src, name)
	switch {
	case err == nil:
		return s, true, nil
	case errors.Is(err, lang.ErrNotFound):
		return lang.Symbol{}, false, nil
	default:
		return lang.Symbol{}, false, err
	}
}

// splice replaces the symbol if it exists, else inserts it after the anchor or at the end.
func splice(a lang.Adapter, base []byte, t Task, code string) ([]byte, error) {
	cur, found, err := findSymbol(a, base, t.Symbol)
	if err != nil {
		return nil, err
	}
	if found {
		return edit.Replace(base, cur.Start, cur.End, code)
	}
	if t.Anchor != "" {
		anchor, err := a.Find(base, t.Anchor)
		if err != nil {
			return nil, fmt.Errorf("anchor: %w", err)
		}
		return edit.InsertAfter(base, anchor.Start, anchor.End, code)
	}
	return edit.Append(base, code), nil
}

// extraSymbols lists symbols in after that are neither in before nor the target
// (methods nested in the target, e.g. of a class, are allowed).
func extraSymbols(before, after []lang.Symbol, target string) []string {
	var names []string
	for _, s := range before {
		names = append(names, s.Name)
	}
	var extra []string
	for _, s := range after {
		if s.Name == target || strings.HasPrefix(s.Name, target+".") || slices.Contains(names, s.Name) {
			continue
		}
		extra = append(extra, s.Name)
	}
	return extra
}

func generate(ctx context.Context, env Env, system string, msgs []providers.Message, usage *providers.Usage) (providers.GeneratedCode, string, error) {
	release, err := env.Acquire(ctx)
	if err != nil {
		return providers.GeneratedCode{}, "", err
	}
	defer release()
	if d := env.Limits.GenerateTimeout.D(); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	var gen providers.GeneratedCode
	_, u, err := providers.Structured(ctx, env.Generator, providers.Request{
		System:     system,
		Messages:   msgs,
		Schema:     providers.GeneratorSchema(),
		SchemaName: "generated_code",
		MaxTokens:  env.OutputReserve,
	}, &gen)
	usage.PromptTokens += u.PromptTokens
	usage.CompletionTokens += u.CompletionTokens
	if err != nil {
		return gen, "", err
	}
	// Fix rounds send back a canonical form of the accepted answer.
	answer, _ := json.Marshal(gen)
	return gen, string(answer), nil
}

func buildPrompt(ctx context.Context, env Env, t Task, base []byte, current lang.Symbol, found bool, system string) (string, []string, error) {
	a := env.Adapter
	var parts []budget.Part
	if env.Style != "" {
		parts = append(parts, budget.Part{Key: "style", Priority: budget.Style, Text: "## Project style guide\n\n" + env.Style + "\n"})
	}
	if syms, err := a.Symbols(base); err == nil {
		var sigs []string
		for _, s := range syms {
			if s.Name != t.Symbol {
				sigs = append(sigs, s.Signature)
			}
		}
		if len(sigs) > 0 {
			parts = append(parts, budget.Part{Key: "outline", Priority: budget.Outline,
				Text: fmt.Sprintf("## Other declarations in %s (signatures only)\n\n```\n%s\n```\n", t.File, strings.Join(sigs, "\n\n"))})
		}
	}
	if imps, err := a.Imports(base); err == nil && len(imps) > 0 {
		parts = append(parts, budget.Part{Key: "imports", Priority: budget.Imports,
			Text: fmt.Sprintf("## Imports of %s\n\n```\n%s\n```\n", t.File, strings.Join(imps, "\n"))})
	}
	for _, s := range env.Context {
		parts = append(parts, budget.Part{Key: "context:" + s.Ref, Priority: budget.ContextSymbols,
			Text: fmt.Sprintf("## Context: %s\n\n```\n%s\n```\n", s.Ref, s.Source)})
	}

	var task strings.Builder
	fmt.Fprintf(&task, "## Task\n\nWrite the %s `%s` in `%s`.\n\n", kindOr(t.Kind), t.Symbol, t.File)
	fmt.Fprintf(&task, "Specification:\n%s\n\n", strings.TrimSpace(t.Spec))
	if strings.TrimSpace(t.Interface) != "" {
		fmt.Fprintf(&task, "Interface (must match exactly):\n```\n%s\n```\n\n", strings.TrimSpace(t.Interface))
	}
	if found {
		fmt.Fprintf(&task, "Current code, which your answer replaces entirely:\n```\n%s\n```\n", base[current.Start:current.End])
	} else if t.Anchor != "" {
		fmt.Fprintf(&task, "This is a new symbol; it will be inserted after `%s`.\n", t.Anchor)
	} else {
		task.WriteString("This is a new symbol; it will be appended to the file.\n")
	}
	parts = append(parts,
		budget.Part{Key: "task", Priority: budget.Required, Text: task.String()},
		budget.Part{Key: "system", Priority: budget.Required, Text: system},
	)

	fit, err := budget.Fit(ctx, parts, env.SlotCtx-env.OutputReserve, env.Count)
	if err != nil {
		return "", nil, err
	}
	var b bytes.Buffer
	for _, p := range fit.Kept {
		if p.Key == "system" {
			continue
		}
		b.WriteString(p.Text)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), fit.Dropped, nil
}

func kindOr(k string) string {
	if k == "" {
		return "symbol"
	}
	return k
}

func render(name string, data any) (string, error) {
	var b strings.Builder
	if err := prompts.Templates.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}

func errorLines(ds []gate.Diagnostic) []string {
	var out []string
	for _, d := range ds {
		if d.Severity != gate.SeverityError {
			continue
		}
		if len(out) == maxFixDiagnostics {
			out = append(out, "(more errors omitted)")
			break
		}
		out = append(out, d.String())
	}
	return out
}

func countErrors(ds []gate.Diagnostic) int {
	n := 0
	for _, d := range ds {
		if d.Severity == gate.SeverityError {
			n++
		}
	}
	return n
}
