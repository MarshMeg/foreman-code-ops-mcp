package lang

import (
	"errors"
	"fmt"
	"regexp"
)

// Toolchain describes, as data, how the gate fixes, checks and tests files of a language.
// The gate package executes it.
//
// Command arguments may contain placeholders:
//
//	{file}      the file being fixed, relative to the root (fix steps only)
//	{files}     all touched files, relative to the root; an argument equal to it expands to several
//	{packages}  package patterns of the touched files; expands like {files}
//	{overlay}   path of a JSON file mapping changed files to their candidate content
//	{tmp}       a scratch directory for this run
//	{filter}    the test name filter (test step only)
type Toolchain struct {
	// RootMarkers locate the directory commands run in when no service covers a file (go.mod).
	RootMarkers []string `yaml:"root_markers"`
	// Overlay is true when checks read candidate content through {overlay} instead of from disk.
	Overlay bool `yaml:"overlay"`
	// PackagePattern turns a directory relative to the root into a package argument ("./{dir}").
	PackagePattern string `yaml:"package_pattern"`
	// RecursivePattern covers a directory and everything below it ("./{dir}/...").
	RecursivePattern string `yaml:"recursive_pattern"`
	// AllPackages is the argument for the whole root ("./...").
	AllPackages string `yaml:"all_packages"`

	// InsertImports adds the generator's import list to a file.
	InsertImports *Step `yaml:"insert_imports"`
	// Fix steps rewrite a file (stdin → stdout for commands): import fixing, formatting.
	Fix []Step `yaml:"fix"`
	// Check steps report diagnostics; the gate stops at the first failing step.
	Check []Step `yaml:"check"`
	// Test runs tests on request.
	Test *TestStep `yaml:"test"`
}

// Step is one gate step, either a built-in implementation or an external command.
type Step struct {
	Name    string   `yaml:"name"`
	Builtin string   `yaml:"builtin"`
	Run     []string `yaml:"run"`
	// Pattern parses diagnostics from the command output. Named groups: file, line, col, message.
	// Non-matching lines continue the previous diagnostic.
	Pattern string `yaml:"pattern"`
	// Severity of parsed diagnostics: error (default) or warning. Warnings do not fail the gate.
	Severity string `yaml:"severity"`
	// Scope is "touched" (default: packages of the touched files) or "all" (the whole root,
	// so that broken callers of a changed or deleted symbol are caught).
	Scope string `yaml:"scope"`
}

// TestStep runs tests.
type TestStep struct {
	Run []string `yaml:"run"`
	// Filter is appended when a test filter is given, e.g. ["-run", "{filter}"].
	Filter []string `yaml:"filter"`
}

// Validate checks internal consistency (built-in names are checked by the gate).
func (t Toolchain) Validate() error {
	var errs []error
	steps := append([]Step{}, t.Fix...)
	steps = append(steps, t.Check...)
	if t.InsertImports != nil {
		steps = append(steps, *t.InsertImports)
	}
	for _, s := range steps {
		if (s.Builtin == "") == (len(s.Run) == 0) {
			errs = append(errs, fmt.Errorf("step %q: set exactly one of builtin and run", s.Name))
		}
		if s.Pattern != "" {
			if _, err := regexp.Compile(s.Pattern); err != nil {
				errs = append(errs, fmt.Errorf("step %q: pattern: %w", s.Name, err))
			}
		}
		if s.Scope != "" && s.Scope != "touched" && s.Scope != "all" {
			errs = append(errs, fmt.Errorf("step %q: scope must be touched or all", s.Name))
		}
		if s.Severity != "" && s.Severity != "error" && s.Severity != "warning" {
			errs = append(errs, fmt.Errorf("step %q: severity must be error or warning", s.Name))
		}
	}
	if t.Test != nil && len(t.Test.Run) == 0 {
		errs = append(errs, errors.New("test: run is required"))
	}
	return errors.Join(errs...)
}

// Steps returns every step with a name, for listing and validation.
func (t Toolchain) Steps() []Step {
	var out []Step
	if t.InsertImports != nil {
		out = append(out, *t.InsertImports)
	}
	out = append(out, t.Fix...)
	return append(out, t.Check...)
}
