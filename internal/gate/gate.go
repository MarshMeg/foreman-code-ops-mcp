// Package gate runs the deterministic checks on candidate code: import insertion,
// fixers/formatters, then build and lint steps, and tests on request. Candidate content
// lives in a workspace.Changeset; checks read it through an overlay, so real files are
// untouched until the change is committed.
package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/lang"
	"github.com/MarshMeg/foreman-code-ops/internal/project"
	"github.com/MarshMeg/foreman-code-ops/internal/workspace"
)

// maxTestOutput is how much of the test output tail is returned.
const maxTestOutput = 20000

// Gate executes adapter toolchains for one project.
type Gate struct {
	proj   *project.Project
	reg    *lang.Registry
	limits config.Limits
	res    map[string]*regexp.Regexp
}

// New validates that every adapter's built-in steps exist and precompiles patterns.
func New(proj *project.Project, reg *lang.Registry, limits config.Limits) (*Gate, error) {
	g := &Gate{proj: proj, reg: reg, limits: limits, res: map[string]*regexp.Regexp{}}
	for _, a := range reg.All() {
		for _, s := range a.Toolchain().Steps() {
			if s.Builtin != "" {
				if _, ok := builtins[s.Builtin]; !ok {
					return nil, fmt.Errorf("adapter %s: unknown builtin step %q", a.Lang(), s.Builtin)
				}
			}
			if s.Pattern != "" {
				g.res[s.Pattern] = regexp.MustCompile(s.Pattern) // validated by the adapter
			}
		}
	}
	return g, nil
}

// WithLimits returns a copy using different limits (per-call overrides).
func (g *Gate) WithLimits(l config.Limits) *Gate {
	c := *g
	c.limits = l
	return &c
}

// plainEnv runs commands in the real root without overlay or shadow (fix filters).
func (g *Gate) plainEnv(a lang.Adapter, root string) *env {
	root = cleanRoot(root)
	dir := filepath.Join(g.proj.Root, filepath.FromSlash(root))
	return &env{g: g, tc: a.Toolchain(), root: root, realDir: dir, dir: dir, backing: map[string]string{}, release: func() {}}
}

// Prepare inserts imports and runs the fix steps on every changed file, writing the
// results back into cs. It returns diagnostics for code that could not be processed
// (for example, syntax errors).
func (g *Gate) Prepare(ctx context.Context, cs *workspace.Changeset, imports map[string][]string) ([]Diagnostic, error) {
	var diags []Diagnostic
	for _, rel := range cs.Files() {
		a := g.reg.ForFile(rel)
		if a == nil {
			continue
		}
		src, exists, err := cs.Read(rel)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		tc := a.Toolchain()
		steps := tc.Fix
		if len(imports[rel]) > 0 && tc.InsertImports != nil {
			steps = append([]lang.Step{*tc.InsertImports}, steps...)
		}
		out, ds, err := g.runFixSteps(ctx, a, rel, src, steps, imports[rel])
		if err != nil {
			return nil, err
		}
		diags = append(diags, ds...)
		if err := cs.Write(rel, out); err != nil {
			return nil, err
		}
	}
	return capDiagnostics(diags), nil
}

// Unformatted reports which of the given files (on disk) the fix steps would change.
func (g *Gate) Unformatted(ctx context.Context, rels []string) ([]string, []Diagnostic, error) {
	var changed []string
	var diags []Diagnostic
	for _, rel := range rels {
		a := g.reg.ForFile(rel)
		if a == nil {
			continue
		}
		src, err := os.ReadFile(filepath.Join(g.proj.Root, rel))
		if err != nil {
			return nil, nil, err
		}
		out, ds, err := g.runFixSteps(ctx, a, rel, src, a.Toolchain().Fix, nil)
		if err != nil {
			return nil, nil, err
		}
		diags = append(diags, ds...)
		if len(ds) == 0 && !bytes.Equal(out, src) {
			changed = append(changed, rel)
		}
	}
	return changed, diags, nil
}

func (g *Gate) runFixSteps(ctx context.Context, a lang.Adapter, rel string, src []byte, steps []lang.Step, imports []string) ([]byte, []Diagnostic, error) {
	e := g.plainEnv(a, g.rootFor(a, rel))
	sel := e.selector()
	var warnings []Diagnostic
	for _, s := range steps {
		cmd, ok := sel.pick(s)
		if !ok {
			continue
		}
		var out []byte
		var ds []Diagnostic
		var err error
		if s.Builtin != "" {
			job := fileJob{abs: filepath.Join(g.proj.Root, rel), rel: rel, src: src, imports: imports, adapter: a}
			out, ds, err = builtins[s.Builtin](ctx, job, stepName(s))
		} else {
			out, ds, err = g.runFixCommand(ctx, e, s, cmd, rel, src)
		}
		if err != nil {
			return nil, nil, err
		}
		if HasErrors(ds) {
			return src, append(warnings, ds...), nil
		}
		warnings = append(warnings, ds...)
		src = out
	}
	return src, warnings, nil
}

func (g *Gate) runFixCommand(ctx context.Context, e *env, s lang.Step, cmd, rel string, src []byte) ([]byte, []Diagnostic, error) {
	args := expand(s.Run, map[string][]string{"{file}": {relTo(e.root, rel)}})
	args[0] = cmd
	res := e.exec(ctx, args, src, g.limits.CheckTimeout.D())
	if res.failure != nil {
		return src, []Diagnostic{*res.failure.withSource(stepName(s))}, nil
	}
	if res.exitCode != 0 {
		ds := e.parser(s).parse(res.stderr + res.stdout)
		if !HasErrors(ds) {
			ds = append(ds, Diagnostic{File: rel, Severity: SeverityError, Source: stepName(s), Message: fmt.Sprintf("exited with code %d", res.exitCode)})
		}
		return src, ds, nil
	}
	if strings.TrimSpace(res.stdout) == "" && len(bytes.TrimSpace(src)) > 0 {
		return src, []Diagnostic{{File: rel, Severity: SeverityError, Source: stepName(s), Message: "fix step printed no output"}}, nil
	}
	return []byte(res.stdout), nil, nil
}

// Report is the result of Check.
type Report struct {
	OK          bool
	Diagnostics []Diagnostic
	// Checked lists "root: step" entries that ran, for transparency.
	Checked  []string
	Warnings []string
}

// Check runs the check steps for everything cs touches plus the given paths (files or
// directories, relative to the project). With no changes and no paths it checks all
// declared services, or the project root when it has a root marker.
func (g *Gate) Check(ctx context.Context, cs *workspace.Changeset, paths []string) (Report, error) {
	groups, warnings, err := g.groups(cs, paths)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Warnings: warnings}
	if len(groups) == 0 {
		rep.Warnings = append(rep.Warnings, "nothing to check: no files with a language adapter and toolchain")
	}
	ran := map[string]bool{} // adapters sharing a root and command (ts, tsx) run it once
	for _, gr := range groups {
		tc := gr.adapter.Toolchain()
		e, err := g.newEnv(gr.adapter, gr.root, cs)
		if err != nil {
			return Report{}, err
		}
		sel := e.selector()
		touched := gr.packages(tc)
		for _, s := range tc.Check {
			pkgs := touched
			if s.Scope == "all" {
				pkgs = []string{tc.AllPackages}
			}
			if len(pkgs) == 0 && len(gr.files) == 0 {
				continue // e.g. only deleted files, whose packages are gone
			}
			cmd, ok := sel.pick(s)
			if !ok {
				continue
			}
			if s.Scope == "all" {
				key := gr.root + "\x00" + strings.Join(s.Run, "\x00")
				if ran[key] {
					continue
				}
				ran[key] = true
			}
			rep.Checked = append(rep.Checked, gr.root+": "+stepName(s))
			ds := g.runCheck(ctx, e, s, cmd, pkgs, gr.files)
			rep.Diagnostics = append(rep.Diagnostics, ds...)
			if HasErrors(ds) {
				break // later steps would mostly repeat the same errors
			}
		}
		e.close()
	}
	rep.Diagnostics = capDiagnostics(rep.Diagnostics)
	rep.OK = !HasErrors(rep.Diagnostics)
	return rep, nil
}

func (g *Gate) runCheck(ctx context.Context, e *env, s lang.Step, cmd string, pkgs, files []string) []Diagnostic {
	if s.Builtin != "" {
		return []Diagnostic{{Severity: SeverityError, Source: stepName(s), Message: "built-in check steps are not supported"}}
	}
	args := expand(s.Run, map[string][]string{
		"{packages}": pkgs,
		"{files}":    relAll(e.root, files),
		"{overlay}":  {e.overlay},
		"{tmp}":      {e.tmp},
		"{cache}":    {e.cacheDir()},
	})
	args[0] = cmd
	res := e.exec(ctx, args, nil, g.limits.CheckTimeout.D())
	if res.failure != nil {
		return []Diagnostic{*res.failure.withSource(stepName(s))}
	}
	ds := e.parser(s).parse(res.stderr + res.stdout)
	if res.exitCode == 0 {
		// Successful runs may still report warnings.
		var warn []Diagnostic
		for _, d := range ds {
			if d.Severity == SeverityWarning && d.File != "" {
				warn = append(warn, d)
			}
		}
		return warn
	}
	if len(ds) == 0 {
		msg := fmt.Sprintf("exited with code %d", res.exitCode)
		if out := strings.TrimSpace(res.stderr + res.stdout); out != "" {
			msg += ": " + tail(out, 1500)
		}
		ds = []Diagnostic{{Severity: SeverityError, Source: stepName(s), Message: msg}}
	}
	if severity(s) == SeverityError && !HasErrors(ds) {
		// A failing command must fail the gate even if its output parsed as warnings.
		ds = append(ds, Diagnostic{Severity: SeverityError, Source: stepName(s), Message: fmt.Sprintf("exited with code %d", res.exitCode)})
	}
	return ds
}

// TestResult is the outcome of a test run.
type TestResult struct {
	Ran     bool   `json:"ran"`
	Passed  bool   `json:"passed"`
	Command string `json:"command,omitempty"`
	Output  string `json:"output,omitempty" jsonschema:"tail of the combined output"`
}

// Test runs the adapter's test step for target (a file, a directory, or a pattern passed
// through verbatim; empty means the whole root) with an optional name filter. cs may hold
// candidate changes the tests should see.
func (g *Gate) Test(ctx context.Context, cs *workspace.Changeset, target, filter string) (TestResult, error) {
	a, root, pkgs, err := g.testTarget(target)
	if err != nil {
		return TestResult{}, err
	}
	e, err := g.newEnv(a, root, cs)
	if err != nil {
		return TestResult{}, err
	}
	defer e.close()
	var step *lang.TestStep
	var cmd string
	for i := range a.Toolchain().Tests {
		t := &a.Toolchain().Tests[i]
		if !e.applies(t.When) {
			continue
		}
		if c, ok := e.resolve(t.Run[0]); ok {
			step, cmd = t, c
			break
		}
	}
	if step == nil {
		return TestResult{}, fmt.Errorf("no test runner found for %s in %s", a.Lang(), root)
	}
	argv := append([]string{}, step.Run...)
	if filter != "" {
		argv = append(argv, step.Filter...)
	}
	args := expand(argv, map[string][]string{
		"{packages}": pkgs,
		"{overlay}":  {e.overlay},
		"{tmp}":      {e.tmp},
		"{cache}":    {e.cacheDir()},
		"{filter}":   {filter},
	})
	args[0] = cmd
	res := e.exec(ctx, args, nil, g.limits.TestTimeout.D())
	out := TestResult{Ran: true, Command: strings.Join(append([]string{step.Run[0]}, args[1:]...), " ")}
	if res.failure != nil {
		out.Output = res.failure.Message
		return out, nil
	}
	out.Passed = res.exitCode == 0
	out.Output = tail(e.unshadow(res.stdout+res.stderr), maxTestOutput)
	return out, nil
}

func (g *Gate) testTarget(target string) (lang.Adapter, string, []string, error) {
	hasTests := func(a lang.Adapter) bool { return len(a.Toolchain().Tests) > 0 }
	if target == "" {
		for _, a := range g.reg.All() {
			if !hasTests(a) {
				continue
			}
			if root, ok := g.markerRoot(a, "."); ok {
				return a, root, []string{a.Toolchain().AllPackages}, nil
			}
		}
		for _, s := range g.proj.Workspace.Services {
			if a := g.reg.ForLang(s.Lang); a != nil && hasTests(a) {
				return a, s.Root, []string{a.Toolchain().AllPackages}, nil
			}
		}
		return nil, "", nil, errors.New("no test target: pass target, or declare services in .foreman/workspace.yaml")
	}
	rel, abs, err := g.proj.Resolve(target)
	if err != nil {
		return nil, "", nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, "", nil, fmt.Errorf("test target %q: %w", target, err)
	}
	if !info.IsDir() {
		a := g.reg.ForFile(rel)
		if a == nil {
			return nil, "", nil, fmt.Errorf("no language adapter for %q", rel)
		}
		root := g.rootFor(a, rel)
		return a, root, []string{pattern(a.Toolchain().PackagePattern, relTo(root, path.Dir(rel)))}, nil
	}
	for _, a := range g.adaptersForDir(rel) {
		if hasTests(a) {
			root := g.rootFor(a, rel+"/x")
			return a, root, []string{pattern(a.Toolchain().RecursivePattern, relTo(root, rel))}, nil
		}
	}
	return nil, "", nil, fmt.Errorf("no adapter with a test step for directory %q", rel)
}
