// Package pipeline produces a checked change for one symbol: it builds the generator
// prompt within the slot budget, generates code, splices it into the file, runs the gate,
// loops on build/lint errors, has the validator review the result and, when the generator
// cannot get it right, lets the validator write it. It does not touch the real file or
// git; the caller commits the returned changeset.
package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/MarshMeg/foreman-code-ops/internal/budget"
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
	// Direct skips the generator: the validator writes the symbol (mode=direct).
	Direct    bool
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
	if i := strings.LastIndex(t.Symbol, "."); i > 0 {
		return insertMember(a, base, t.Symbol[:i], code)
	}
	return edit.Append(base, code), nil
}

// insertMember places a new member (Type.Method) after the type's last member, or after the
// type itself where methods are top-level declarations (Go).
func insertMember(a lang.Adapter, base []byte, parent, code string) ([]byte, error) {
	syms, err := a.Symbols(base)
	if err != nil {
		return nil, err
	}
	var owner, last *lang.Symbol
	for i := range syms {
		s := &syms[i]
		switch {
		case s.Name == parent:
			owner = s
		case strings.HasPrefix(s.Name, parent+".") && !strings.Contains(strings.TrimPrefix(s.Name, parent+"."), "."):
			last = s
		}
	}
	switch {
	case last != nil:
		return edit.InsertAfter(base, last.Start, last.End, code)
	case !a.NestedMembers() && owner == nil:
		// Go: the type may be declared in another file of the package.
		return edit.Append(base, code), nil
	case !a.NestedMembers():
		return edit.InsertAfter(base, owner.Start, owner.End, code)
	case owner == nil:
		return nil, fmt.Errorf("%s does not exist in the file; create it first or pass anchor", parent)
	default:
		return nil, fmt.Errorf("%s has no members to insert after; pass anchor or rewrite %s as a whole", parent, parent)
	}
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

// buildPrompt renders the writer prompt, dropping low-priority parts to fit limit tokens.
func buildPrompt(ctx context.Context, env Env, t Task, base []byte, current lang.Symbol, found bool, system string, limit int, count budget.Counter) (string, []string, error) {
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

	fit, err := budget.Fit(ctx, parts, limit, count)
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
