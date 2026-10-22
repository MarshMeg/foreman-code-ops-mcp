package lang

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
//	{cache}     a stable per-root cache directory (incremental builds)
//	{filter}    the test name filter (test step only)
type Toolchain struct {
	// RootMarkers locate the directory commands run in (go.mod, pyproject.toml, tsconfig.json);
	// globs are allowed (*.csproj).
	RootMarkers []string `yaml:"root_markers"`
	// BinDirs are directories under the root searched for commands before PATH
	// (node_modules/.bin, .venv/bin).
	BinDirs []string `yaml:"bin_dirs"`
	// Overlay is true when checks read candidate content through {overlay}. Otherwise the gate
	// runs commands in a shadow copy of the root (symlinks plus the changed files).
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
	// Tests run on request; the first applicable entry is used.
	Tests []TestStep `yaml:"tests"`
	// LSP lists language servers that give fast diagnostics for candidate files before the
	// check steps (when lsp.enabled). The first that applies and starts is used.
	LSP []LSPServer `yaml:"lsp"`
}

// LSPServer is a language server used for diagnostics.
type LSPServer struct {
	Name string   `yaml:"name"`
	Run  []string `yaml:"run"`
	// LanguageIDs maps file extensions to LSP language ids (.tsx: typescriptreact).
	LanguageIDs map[string]string `yaml:"language_ids"`
	// InitOptions are sent as initializationOptions.
	InitOptions map[string]any `yaml:"init_options"`
	// SettleMS is how long to wait for further pushed diagnostics after the first (servers
	// without pull diagnostics); default 500.
	SettleMS int       `yaml:"settle_ms"`
	When     Condition `yaml:"when"`
}

// Condition restricts a step to projects that use a tool.
type Condition struct {
	// Files: at least one exists in the root.
	Files []string `yaml:"files"`
	// NoFiles: none exists in the root.
	NoFiles []string `yaml:"no_files"`
	// Deps: package.json lists at least one in dependencies or devDependencies.
	Deps []string `yaml:"deps"`
	// Contains: file → substring; at least one file contains its substring.
	Contains map[string]string `yaml:"contains"`
}

// IsZero reports whether the condition is empty (always true).
func (c Condition) IsZero() bool {
	return len(c.Files) == 0 && len(c.NoFiles) == 0 && len(c.Deps) == 0 && len(c.Contains) == 0
}

// Step is one gate step, either a built-in implementation or an external command.
type Step struct {
	Name    string   `yaml:"name"`
	Builtin string   `yaml:"builtin"`
	Run     []string `yaml:"run"`
	// Pattern parses diagnostics from the command output. Named groups: file, line, col,
	// message, and optionally severity (a value containing "warn" makes a warning).
	// Non-matching lines continue the previous diagnostic.
	Pattern string `yaml:"pattern"`
	// Severity of parsed diagnostics: error (default) or warning. Warnings do not fail the gate.
	Severity string `yaml:"severity"`
	// Scope is "touched" (default: packages of the touched files) or "all" (the whole root,
	// so that broken callers of a changed or deleted symbol are caught).
	Scope string `yaml:"scope"`
	// When restricts the step to projects matching the condition.
	When Condition `yaml:"when"`
	// Optional skips the step silently when its command is not installed.
	Optional bool `yaml:"optional"`
	// Group: of the applicable steps sharing a group, only the first runs (pyright or mypy).
	Group string `yaml:"group"`
	// OnlyMatches drops output lines that do not match Pattern (progress, summaries).
	OnlyMatches bool `yaml:"only_matches"`
	// Parser selects a structured output parser instead of Pattern: "eslint-json".
	Parser string `yaml:"parser"`
}

// TestStep runs tests.
type TestStep struct {
	Name string   `yaml:"name"`
	Run  []string `yaml:"run"`
	// Filter is appended when a test filter is given, e.g. ["-run", "{filter}"].
	Filter []string  `yaml:"filter"`
	When   Condition `yaml:"when"`
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
		if s.Parser != "" && s.Parser != "eslint-json" {
			errs = append(errs, fmt.Errorf("step %q: unknown parser %q", s.Name, s.Parser))
		}
		if s.OnlyMatches && s.Pattern == "" {
			errs = append(errs, fmt.Errorf("step %q: only_matches needs a pattern", s.Name))
		}
	}
	for i, ts := range t.Tests {
		if len(ts.Run) == 0 {
			errs = append(errs, fmt.Errorf("tests[%d]: run is required", i))
		}
	}
	for i, l := range t.LSP {
		if len(l.Run) == 0 || l.Name == "" {
			errs = append(errs, fmt.Errorf("lsp[%d]: name and run are required", i))
		}
		if len(l.LanguageIDs) == 0 {
			errs = append(errs, fmt.Errorf("lsp %q: language_ids is required", l.Name))
		}
		if l.SettleMS < 0 {
			errs = append(errs, fmt.Errorf("lsp %q: settle_ms must be >= 0", l.Name))
		}
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

// HasMarker reports whether dir contains a file matching one of markers (names or globs).
func HasMarker(dir string, markers []string) bool {
	for _, m := range markers {
		if strings.ContainsAny(m, "*?[") {
			if found, _ := filepath.Glob(filepath.Join(dir, m)); len(found) > 0 {
				return true
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}
