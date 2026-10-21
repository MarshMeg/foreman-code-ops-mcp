package ops

import (
	"context"
	"os"

	"github.com/MarshMeg/foreman-code-ops/internal/gate"
	"github.com/MarshMeg/foreman-code-ops/internal/workspace"
)

// Check runs the gate on files as they are on disk. Explicitly listed files are also
// checked for formatting and imports; directories and the default whole-project run only
// build and lint.
func (o *Ops) Check(ctx context.Context, paths []string) (gate.Report, error) {
	rep, err := o.gate.Check(ctx, workspace.New(o.proj.Root), paths)
	if err != nil {
		return rep, err
	}
	var files []string
	for _, p := range paths {
		rel, abs, err := o.proj.Resolve(p)
		if err != nil {
			return rep, err
		}
		if info, err := os.Stat(abs); err == nil && info.Mode().IsRegular() {
			files = append(files, rel)
		}
	}
	unformatted, diags, err := o.gate.Unformatted(ctx, files)
	if err != nil {
		return rep, err
	}
	for _, f := range unformatted {
		rep.Diagnostics = append(rep.Diagnostics, gate.Diagnostic{
			File: f, Severity: gate.SeverityWarning, Source: "format",
			Message: "file is not formatted or has unsorted/unused imports; the next foreman edit will fix it",
		})
	}
	rep.Diagnostics = append(rep.Diagnostics, diags...)
	rep.OK = !gate.HasErrors(rep.Diagnostics)
	return rep, nil
}

// RunTests runs tests on files as they are on disk.
func (o *Ops) RunTests(ctx context.Context, target, filter string) (gate.TestResult, error) {
	return o.gate.Test(ctx, workspace.New(o.proj.Root), target, filter)
}

// ToolWarnings lists required toolchain commands that are missing for languages the project uses.
func (o *Ops) ToolWarnings() []string { return o.gate.MissingTools() }
