// Package gate runs the deterministic checks on candidate code: import insertion,
// fixers/formatters, then build and lint steps, and tests on request. Candidate content
// lives in a workspace.Changeset; checks read it through an overlay, so real files are
// untouched until the change is committed.
package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

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
	root := g.rootFor(a, rel)
	for _, s := range steps {
		var out []byte
		var ds []Diagnostic
		var err error
		if s.Builtin != "" {
			job := fileJob{abs: filepath.Join(g.proj.Root, rel), rel: rel, src: src, imports: imports}
			out, ds, err = builtins[s.Builtin](ctx, job, stepName(s))
		} else {
			out, ds, err = g.runFixCommand(ctx, s, root, rel, src)
		}
		if err != nil {
			return nil, nil, err
		}
		if HasErrors(ds) {
			return src, ds, nil
		}
		src = out
	}
	return src, nil, nil
}

func (g *Gate) runFixCommand(ctx context.Context, s lang.Step, root, rel string, src []byte) ([]byte, []Diagnostic, error) {
	fileArg := relTo(root, rel)
	args := expand(s.Run, map[string][]string{"{file}": {fileArg}})
	res := g.exec(ctx, root, args, src, g.limits.CheckTimeout.D())
	if res.failure != nil {
		return src, []Diagnostic{*res.failure.withSource(stepName(s))}, nil
	}
	if res.exitCode != 0 {
		return src, g.parser(s, root).parse(res.stderr + res.stdout), nil
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
	run, err := g.newRun(cs)
	if err != nil {
		return Report{}, err
	}
	defer run.close()
	for _, gr := range groups {
		tc := gr.adapter.Toolchain()
		if !cs.Empty() && !tc.Overlay {
			return Report{}, fmt.Errorf("adapter %s cannot check unapplied changes (no overlay support)", gr.adapter.Lang())
		}
		touched := gr.packages(tc)
		for _, s := range tc.Check {
			pkgs := touched
			if s.Scope == "all" {
				pkgs = []string{tc.AllPackages}
			}
			if len(pkgs) == 0 {
				continue // e.g. only deleted files, whose packages are gone
			}
			rep.Checked = append(rep.Checked, gr.root+": "+stepName(s))
			ds := g.runCheck(ctx, run, s, gr.root, pkgs, gr.files)
			rep.Diagnostics = append(rep.Diagnostics, ds...)
			if HasErrors(ds) {
				break // later steps would mostly repeat the same errors
			}
		}
	}
	rep.Diagnostics = capDiagnostics(rep.Diagnostics)
	rep.OK = !HasErrors(rep.Diagnostics)
	return rep, nil
}

func (g *Gate) runCheck(ctx context.Context, run *runDir, s lang.Step, root string, pkgs, files []string) []Diagnostic {
	if s.Builtin != "" {
		return []Diagnostic{{Severity: SeverityError, Source: stepName(s), Message: "built-in check steps are not supported"}}
	}
	args := expand(s.Run, map[string][]string{
		"{packages}": pkgs,
		"{files}":    relAll(root, files),
		"{overlay}":  {run.overlay},
		"{tmp}":      {run.tmp},
	})
	res := g.exec(ctx, root, args, nil, g.limits.CheckTimeout.D())
	if res.failure != nil {
		return []Diagnostic{*res.failure.withSource(stepName(s))}
	}
	if res.exitCode == 0 {
		return nil
	}
	p := g.parser(s, root)
	p.backing = run.backing
	ds := p.parse(res.stderr + res.stdout)
	if len(ds) == 0 {
		ds = []Diagnostic{{Severity: SeverityError, Source: stepName(s), Message: fmt.Sprintf("exited with code %d", res.exitCode)}}
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
	tc := a.Toolchain()
	if tc.Test == nil {
		return TestResult{}, fmt.Errorf("adapter %s has no test step", a.Lang())
	}
	if !cs.Empty() && !tc.Overlay {
		return TestResult{}, fmt.Errorf("adapter %s cannot test unapplied changes (no overlay support)", a.Lang())
	}
	run, err := g.newRun(cs)
	if err != nil {
		return TestResult{}, err
	}
	defer run.close()
	argv := append([]string{}, tc.Test.Run...)
	if filter != "" {
		argv = append(argv, tc.Test.Filter...)
	}
	args := expand(argv, map[string][]string{
		"{packages}": pkgs,
		"{overlay}":  {run.overlay},
		"{tmp}":      {run.tmp},
		"{filter}":   {filter},
	})
	res := g.exec(ctx, root, args, nil, g.limits.TestTimeout.D())
	out := TestResult{Ran: true, Command: strings.Join(args, " ")}
	if res.failure != nil {
		out.Output = res.failure.Message
		return out, nil
	}
	out.Passed = res.exitCode == 0
	output := res.stdout + res.stderr
	for backing, rel := range run.backing {
		output = strings.ReplaceAll(output, backing, rel)
	}
	out.Output = tail(output, maxTestOutput)
	return out, nil
}

func (g *Gate) testTarget(target string) (lang.Adapter, string, []string, error) {
	if target == "" {
		for _, a := range g.reg.All() {
			if a.Toolchain().Test == nil {
				continue
			}
			if root, ok := g.markerRoot(a, "."); ok {
				return a, root, []string{a.Toolchain().AllPackages}, nil
			}
		}
		for _, s := range g.proj.Workspace.Services {
			if a := g.reg.ForLang(s.Lang); a != nil && a.Toolchain().Test != nil {
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
		if a.Toolchain().Test != nil {
			root := g.rootFor(a, rel+"/x")
			return a, root, []string{pattern(a.Toolchain().RecursivePattern, relTo(root, rel))}, nil
		}
	}
	return nil, "", nil, fmt.Errorf("no adapter with a test step for directory %q", rel)
}

// group is a set of files and directories checked together in one root.
type group struct {
	adapter lang.Adapter
	root    string   // project-relative
	files   []string // project-relative files (existing after the change)
	dirs    []string // project-relative directories checked recursively
	all     bool
	deleted bool // a package disappeared; only scope=all steps can see the fallout
}

func (gr *group) packages(tc lang.Toolchain) []string {
	if gr.all {
		return []string{tc.AllPackages}
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, d := range gr.dirs {
		add(pattern(tc.RecursivePattern, relTo(gr.root, d)))
	}
	for _, f := range gr.files {
		add(pattern(tc.PackagePattern, relTo(gr.root, path.Dir(f))))
	}
	sort.Strings(out)
	return out
}

func (g *Gate) groups(cs *workspace.Changeset, paths []string) ([]*group, []string, error) {
	byKey := map[string]*group{}
	var order []string
	get := func(a lang.Adapter, root string) *group {
		key := a.Lang() + "\x00" + root
		if gr, ok := byKey[key]; ok {
			return gr
		}
		gr := &group{adapter: a, root: root}
		byKey[key] = gr
		order = append(order, key)
		return gr
	}
	var warnings []string
	addFile := func(rel string, exists bool) {
		a := g.reg.ForFile(rel)
		if a == nil || len(a.Toolchain().Check) == 0 {
			return
		}
		gr := get(a, g.rootFor(a, rel))
		if exists || g.dirHasLangFiles(cs, a, path.Dir(rel)) {
			gr.files = append(gr.files, rel)
		} else {
			gr.deleted = true
		}
	}

	for _, rel := range cs.Files() {
		_, exists, err := cs.Read(rel)
		if err != nil {
			return nil, nil, err
		}
		addFile(rel, exists)
	}
	for _, p := range paths {
		rel, abs, err := g.proj.Resolve(p)
		if err != nil {
			return nil, nil, err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, nil, fmt.Errorf("check path %q: %w", p, err)
		}
		if !info.IsDir() {
			addFile(rel, true)
			continue
		}
		adapters := g.adaptersForDir(rel)
		if len(adapters) == 0 {
			warnings = append(warnings, fmt.Sprintf("no language detected for directory %q", rel))
		}
		for _, a := range adapters {
			gr := get(a, g.rootFor(a, rel+"/x"))
			gr.dirs = append(gr.dirs, rel)
		}
	}
	if cs.Empty() && len(paths) == 0 {
		for _, a := range g.reg.All() {
			if len(a.Toolchain().Check) == 0 {
				continue
			}
			for _, s := range g.proj.Workspace.Services {
				if s.Lang == a.Lang() {
					get(a, cleanRoot(s.Root)).all = true
				}
			}
			if len(g.proj.Workspace.Services) == 0 {
				if root, ok := g.markerRoot(a, "."); ok {
					get(a, root).all = true
				}
			}
		}
	}
	out := make([]*group, 0, len(order))
	for _, k := range order {
		if gr := byKey[k]; gr.all || gr.deleted || len(gr.files) > 0 || len(gr.dirs) > 0 {
			out = append(out, gr)
		}
	}
	return out, warnings, nil
}

// rootFor returns the project-relative directory commands for rel run in: the owning
// service when its language matches, else the nearest ancestor with a root marker,
// else the project root.
func (g *Gate) rootFor(a lang.Adapter, rel string) string {
	if s := g.proj.ServiceFor(rel); s != nil && (s.Lang == "" || s.Lang == a.Lang()) {
		return cleanRoot(s.Root)
	}
	if root, ok := g.markerRoot(a, path.Dir(rel)); ok {
		return root
	}
	return "."
}

// markerRoot walks up from dir looking for one of the adapter's root markers.
func (g *Gate) markerRoot(a lang.Adapter, dir string) (string, bool) {
	markers := a.Toolchain().RootMarkers
	for d := dir; ; d = path.Dir(d) {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(g.proj.Root, filepath.FromSlash(d), m)); err == nil {
				return d, true
			}
		}
		if d == "." || d == "/" {
			return "", false
		}
	}
}

// adaptersForDir picks adapters for a directory: the owning service's language, or every
// adapter with a root marker at or above it.
func (g *Gate) adaptersForDir(rel string) []lang.Adapter {
	if s := g.proj.ServiceFor(rel + "/x"); s != nil {
		if a := g.reg.ForLang(s.Lang); a != nil {
			return []lang.Adapter{a}
		}
	}
	var out []lang.Adapter
	for _, a := range g.reg.All() {
		if _, ok := g.markerRoot(a, rel); ok {
			out = append(out, a)
		}
	}
	return out
}

// dirHasLangFiles reports whether dir still holds files of the adapter's language after cs.
func (g *Gate) dirHasLangFiles(cs *workspace.Changeset, a lang.Adapter, dir string) bool {
	entries, _ := os.ReadDir(filepath.Join(g.proj.Root, filepath.FromSlash(dir)))
	for _, e := range entries {
		rel := path.Join(dir, e.Name())
		if e.Type().IsRegular() && g.reg.ForFile(rel) == a {
			if _, exists, _ := cs.Read(rel); exists {
				return true
			}
		}
	}
	for _, rel := range cs.Files() {
		if path.Dir(rel) == dir && g.reg.ForFile(rel) == a {
			if _, exists, _ := cs.Read(rel); exists {
				return true
			}
		}
	}
	return false
}

// runDir holds per-run scratch space and the overlay file.
type runDir struct {
	tmp, overlay string
	backing      map[string]string // backing file → project-relative path
}

func (g *Gate) newRun(cs *workspace.Changeset) (*runDir, error) {
	tmp, err := os.MkdirTemp("", "foreman-gate-")
	if err != nil {
		return nil, err
	}
	// Tools report resolved paths (macOS /var -> /private/var); match them.
	if real, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = real
	}
	run := &runDir{tmp: tmp, overlay: filepath.Join(tmp, "overlay.json"), backing: map[string]string{}}
	replace := map[string]string{}
	for i, rel := range cs.Files() {
		content, exists, err := cs.Read(rel)
		if err != nil {
			run.close()
			return nil, err
		}
		abs := filepath.Join(g.proj.Root, filepath.FromSlash(rel))
		if !exists {
			replace[abs] = ""
			continue
		}
		backing := filepath.Join(tmp, fmt.Sprintf("f%d%s", i, filepath.Ext(rel)))
		if err := os.WriteFile(backing, content, 0o644); err != nil {
			run.close()
			return nil, err
		}
		replace[abs] = backing
		run.backing[backing] = rel
	}
	data, _ := json.Marshal(map[string]any{"Replace": replace})
	if err := os.WriteFile(run.overlay, data, 0o644); err != nil {
		run.close()
		return nil, err
	}
	return run, nil
}

func (r *runDir) close() { os.RemoveAll(r.tmp) }

type execResult struct {
	stdout, stderr string
	exitCode       int
	// failure is set when the command could not run or timed out.
	failure *Diagnostic
}

func (d *Diagnostic) withSource(s string) *Diagnostic {
	d.Source = s
	return d
}

func (g *Gate) exec(ctx context.Context, root string, args []string, stdin []byte, timeout time.Duration) execResult {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = filepath.Join(g.proj.Root, filepath.FromSlash(root))
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := execResult{stdout: stdout.String(), stderr: stderr.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() == context.DeadlineExceeded:
		res.failure = &Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("%s timed out after %s", args[0], timeout)}
	case errors.As(err, &ee):
		res.exitCode = ee.ExitCode()
	case errors.Is(err, exec.ErrNotFound):
		res.failure = &Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("%s not found in PATH", args[0])}
	default:
		res.failure = &Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("run %s: %v", args[0], err)}
	}
	return res
}

func (g *Gate) parser(s lang.Step, root string) outputParser {
	return outputParser{
		re:          g.res[s.Pattern],
		source:      stepName(s),
		severity:    severity(s),
		projectRoot: g.proj.Root,
		cwd:         filepath.Join(g.proj.Root, filepath.FromSlash(root)),
	}
}

func severity(s lang.Step) string {
	if s.Severity == "" {
		return SeverityError
	}
	return s.Severity
}

func stepName(s lang.Step) string {
	switch {
	case s.Name != "":
		return s.Name
	case s.Builtin != "":
		return s.Builtin
	default:
		return strings.Join(s.Run, " ")
	}
}

// expand substitutes placeholders. An argument equal to a placeholder is replaced by all its
// values (possibly none); placeholders inside a longer argument take the first value.
func expand(argv []string, vars map[string][]string) []string {
	var out []string
	for _, arg := range argv {
		if vals, ok := vars[arg]; ok {
			out = append(out, vals...)
			continue
		}
		for k, vals := range vars {
			if strings.Contains(arg, k) && len(vals) > 0 {
				arg = strings.ReplaceAll(arg, k, vals[0])
			}
		}
		out = append(out, arg)
	}
	return out
}

func pattern(p, dir string) string {
	if p == "" {
		p = "{dir}"
	}
	return strings.ReplaceAll(p, "{dir}", dir)
}

// relTo makes a project-relative path relative to a project-relative root.
func relTo(root, rel string) string {
	root = cleanRoot(root)
	if root == "." {
		return rel
	}
	if rel == root {
		return "."
	}
	return strings.TrimPrefix(rel, root+"/")
}

func relAll(root string, rels []string) []string {
	out := make([]string, len(rels))
	for i, r := range rels {
		out[i] = relTo(root, r)
	}
	return out
}

func cleanRoot(r string) string {
	if r == "" {
		return "."
	}
	return path.Clean(r)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
