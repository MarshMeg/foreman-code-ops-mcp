package mcp

import (
	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/gate"
	"github.com/MarshMeg/foreman-code-ops/internal/links"
)

// SymbolRef points to a symbol whose source the server resolves itself.
type SymbolRef struct {
	File   string `json:"file" jsonschema:"path relative to the project root"`
	Symbol string `json:"symbol" jsonschema:"symbol name, e.g. BookService.List"`
}

// UpsertSymbolInput is the input of upsert_symbol.
type UpsertSymbolInput struct {
	File          string                 `json:"file" jsonschema:"path relative to the project root; created if missing"`
	Symbol        string                 `json:"symbol" jsonschema:"symbol to create or replace: Func, Type.Method, Class.method, Component"`
	Kind          string                 `json:"kind" jsonschema:"one of: function, method, type, class, component, interface"`
	Spec          string                 `json:"spec" jsonschema:"what the symbol must do, in English"`
	Interface     string                 `json:"interface" jsonschema:"signature, types, errors or props the code must match"`
	Context       []SymbolRef            `json:"context,omitempty" jsonschema:"symbols the writer should see; pass references, not source text"`
	Anchor        string                 `json:"anchor,omitempty" jsonschema:"for new symbols: insert after this symbol"`
	Mode          string                 `json:"mode,omitempty" jsonschema:"local (default): generator writes, validator reviews; direct: validator writes"`
	Package       string                 `json:"package,omitempty" jsonschema:"package name for a new Go file"`
	RunTests      any                    `json:"run_tests,omitempty" jsonschema:"true runs the package/module tests; a string is a test filter"`
	CommitMessage string                 `json:"commit_message,omitempty" jsonschema:"commit subject; generated if omitted"`
	Limits        *config.LimitsOverride `json:"limits,omitempty" jsonschema:"per-call override of loop limits"`
}

// Attempts counts loop iterations.
type Attempts struct {
	BuildFixes int `json:"build_fixes"`
	Reviews    int `json:"reviews"`
}

// Diagnostic is a normalized gate finding.
type Diagnostic = gate.Diagnostic

// TestResult summarizes a requested test run.
type TestResult = gate.TestResult

// UpsertSymbolOutput is the result of upsert_symbol.
type UpsertSymbolOutput struct {
	Status      string       `json:"status" jsonschema:"applied, failed (nothing written) or refused (uncommitted user changes)"`
	Message     string       `json:"message,omitempty"`
	Symbol      string       `json:"symbol"`
	Signature   string       `json:"signature,omitempty"`
	Diff        string       `json:"diff,omitempty"`
	Commit      string       `json:"commit,omitempty"`
	WrittenBy   string       `json:"written_by" jsonschema:"local, validator or none"`
	ReviewedBy  string       `json:"reviewed_by,omitempty" jsonschema:"validator that accepted the code; empty when not reviewed"`
	Attempts    Attempts     `json:"attempts"`
	MinorNotes  []string     `json:"minor_notes" jsonschema:"non-blocking review remarks"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Tests       TestResult   `json:"tests"`
	LastCode    string       `json:"last_code,omitempty" jsonschema:"last candidate when status is failed"`
	Warnings    []string     `json:"warnings,omitempty"`

	file string // for the journal
}

// GetSymbolOutput is the result of get_symbol.
type GetSymbolOutput struct {
	File      string `json:"file"`
	Symbol    string `json:"symbol"`
	Kind      string `json:"kind"`
	Signature string `json:"signature"`
	Source    string `json:"source"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// DeleteSymbolInput is the input of delete_symbol.
type DeleteSymbolInput struct {
	File   string `json:"file" jsonschema:"path relative to the project root"`
	Symbol string `json:"symbol" jsonschema:"symbol to delete"`
	Force  bool   `json:"force,omitempty" jsonschema:"delete even if references are found or the build breaks"`
}

// CreateFileInput is the input of create_file.
type CreateFileInput struct {
	Path    string `json:"path" jsonschema:"path relative to the project root"`
	Package string `json:"package,omitempty" jsonschema:"package name for a new Go file"`
}

// DeleteFileInput is the input of delete_file.
type DeleteFileInput struct {
	Path  string `json:"path" jsonschema:"path relative to the project root"`
	Force bool   `json:"force,omitempty" jsonschema:"delete even if the build breaks without the file"`
}

// ChangeOutput is the result of operations that edit and commit files.
type ChangeOutput struct {
	Status      string       `json:"status" jsonschema:"applied, unchanged, refused (nothing written) or failed (written but not committed)"`
	Message     string       `json:"message,omitempty" jsonschema:"why the operation was refused or failed"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty" jsonschema:"gate findings for the change"`
	Diff        string       `json:"diff,omitempty"`
	Commit      string       `json:"commit,omitempty"`
	References  []string     `json:"references,omitempty" jsonschema:"places that still use a deleted symbol"`
	Warnings    []string     `json:"warnings,omitempty"`
}

// CheckInput is the input of check.
type CheckInput struct {
	Paths []string `json:"paths,omitempty" jsonschema:"files or directories to check; default: whole project"`
}

// CheckOutput is the result of check.
type CheckOutput struct {
	OK          bool         `json:"ok" jsonschema:"false when any diagnostic is an error"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Checked     []string     `json:"checked" jsonschema:"root: step pairs that ran"`
	Warnings    []string     `json:"warnings,omitempty"`
}

// RunTestsInput is the input of run_tests.
type RunTestsInput struct {
	Target string `json:"target,omitempty" jsonschema:"package, module or directory; default: whole project"`
	Filter string `json:"filter,omitempty" jsonschema:"test name filter (go test -run, pytest -k, vitest -t)"`
}

// StyleOutput is the result of style_get.
type StyleOutput struct {
	Exists   bool     `json:"exists"`
	Content  string   `json:"content"`
	Sections []string `json:"sections"`
}

// StyleSetInput is the input of style_set.
type StyleSetInput struct {
	Section string `json:"section,omitempty" jsonschema:"replace only this '## Section'; omit to replace the whole file"`
	Content string `json:"content" jsonschema:"Markdown, in English"`
}

// StatusOutput is the result of status.
type StatusOutput struct {
	Version   string          `json:"version"`
	Project   string          `json:"project"`
	Generator GeneratorStatus `json:"generator"`
	Validator ValidatorStatus `json:"validator"`
	Limits    config.Limits   `json:"limits"`
	Graph     GraphStatus     `json:"graph"`
	Queue     QueueStatus     `json:"queue"`
	Warnings  []string        `json:"warnings"`
}

type GeneratorStatus struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	Slots    int    `json:"slots"`
	SlotCtx  int    `json:"slot_ctx"`
}

type ValidatorStatus struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	Enabled  bool   `json:"enabled"`
}

type GraphStatus struct {
	Provider  string `json:"provider"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty" jsonschema:"why the graph is unavailable"`
}

type QueueStatus struct {
	Running int `json:"running"`
	Waiting int `json:"waiting"`
}

// DiscoverLinksOutput is the result of discover_links.
type DiscoverLinksOutput struct {
	Status   string   `json:"status" jsonschema:"applied, unchanged, refused or failed"`
	Message  string   `json:"message,omitempty"`
	Commit   string   `json:"commit,omitempty"`
	Services []string `json:"services"`
	Routes   int      `json:"routes"`
	Calls    int      `json:"calls"`
	Links    []string `json:"links" jsonschema:"caller → METHOD path → handler [confidence]"`
	Unlinked []string `json:"unlinked,omitempty" jsonschema:"calls that matched no route"`
}

// GetLinksInput is the input of get_links.
type GetLinksInput struct {
	Service string `json:"service,omitempty" jsonschema:"service name from .foreman/workspace.yaml"`
	File    string `json:"file,omitempty" jsonschema:"project-relative file"`
	Symbol  string `json:"symbol,omitempty" jsonschema:"symbol name, e.g. BookHandler.List or useBooks"`
}

// GetLinksOutput is the result of get_links.
type GetLinksOutput struct {
	Links []links.Link `json:"links"`
}
