package ops

import (
	"context"
	"fmt"
	"os"
	"os/exec"

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

// ToolWarnings lists toolchain commands that are not in PATH.
func (o *Ops) ToolWarnings() []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range o.reg.All() {
		tc := a.Toolchain()
		var cmds [][]string
		for _, s := range tc.Steps() {
			cmds = append(cmds, s.Run)
		}
		if tc.Test != nil {
			cmds = append(cmds, tc.Test.Run)
		}
		for _, c := range cmds {
			if len(c) == 0 || seen[c[0]] {
				continue
			}
			seen[c[0]] = true
			if _, err := exec.LookPath(c[0]); err != nil {
				out = append(out, fmt.Sprintf("%s: %q not found in PATH; its gate steps will fail", a.Lang(), c[0]))
			}
		}
	}
	return out
}
