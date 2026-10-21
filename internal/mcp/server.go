// Package mcp exposes foreman-code-ops operations as MCP tools.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/journal"
	"github.com/MarshMeg/foreman-code-ops/internal/ops"
)

// ServerName is the name clients register the server under (tools become mcp__foreman__*).
const ServerName = "foreman"

const instructions = `foreman-code-ops edits code symbol by symbol. Use upsert_symbol / delete_symbol / create_file / delete_file instead of editing source files directly. ` +
	`A cheaper model writes the code, the build/lint gate and an optional validator check it, and every successful operation is committed. ` +
	`Write spec, interface, commit_message and style content in English. Pass context as symbol references, not source text.`

// Deps are the services the tools use.
type Deps struct {
	Version    string
	ProjectDir string
	Config     config.Config
	Logger     *slog.Logger
	Journal    *journal.Journal
	Ops        *ops.Ops
}

// NewServer builds the MCP server with all tools registered.
func NewServer(d Deps) *sdk.Server {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Journal == nil {
		d.Journal = journal.Discard()
	}
	s := sdk.NewServer(&sdk.Implementation{Name: ServerName, Title: "foreman-code-ops", Version: d.Version},
		&sdk.ServerOptions{Instructions: instructions, Logger: d.Logger})
	h := &handlers{d: d}

	add(h, s, &sdk.Tool{
		Name: "upsert_symbol",
		Description: "Create or replace a function, method, type, class, interface or component. " +
			"The generator writes the code; imports are fixed, the file is formatted, linted and built; the validator (if enabled) reviews it; " +
			"on success the change is committed and the new signature returned. On failure, diagnostics and the last candidate are returned.",
	}, notImplemented[UpsertSymbolInput, UpsertSymbolOutput]("upsert_symbol", 3))

	add(h, s, &sdk.Tool{
		Name:        "get_symbol",
		Description: "Return the source, signature and line range of a symbol.",
	}, h.getSymbol)

	add(h, s, &sdk.Tool{
		Name: "delete_symbol",
		Description: "Delete a symbol, fix imports and commit. Without force, refuses when references are found " +
			"(via the code graph, or a name search when no graph is configured) or when the build or lint fails afterwards.",
	}, h.deleteSymbol)

	add(h, s, &sdk.Tool{
		Name:        "create_file",
		Description: "Create a source file and commit. Go files get a package clause (from the package argument, sibling files or the directory name).",
	}, h.createFile)

	add(h, s, &sdk.Tool{
		Name:        "delete_file",
		Description: "Delete a file and commit. Without force, refuses when the build or lint fails afterwards.",
	}, h.deleteFile)

	add(h, s, &sdk.Tool{
		Name: "check",
		Description: "Run the gate without changing anything: build and lint for the given files/directories (default: whole project); " +
			"listed files are also checked for formatting and imports.",
	}, h.check)

	add(h, s, &sdk.Tool{
		Name: "run_tests",
		Description: "Run tests for a file's package, a directory (recursively) or the whole project, with an optional name filter. " +
			"Results are reported, never auto-fixed.",
	}, h.runTests)

	add(h, s, &sdk.Tool{
		Name:        "style_get",
		Description: "Return the project style guide (.foreman/style.md). If it does not exist, offer the user to create one with style_set.",
	}, h.styleGet)

	add(h, s, &sdk.Tool{
		Name:        "style_set",
		Description: "Create or update .foreman/style.md (whole file or one '## Section') and commit. Write it in English.",
	}, notImplemented[StyleSetInput, StyleOutput]("style_set", 5))

	add(h, s, &sdk.Tool{
		Name:        "status",
		Description: "Report configured providers, generator slots, queue, code graph state and configuration warnings.",
	}, h.status)

	return s
}

type handlers struct{ d Deps }

// add registers a tool and wraps its handler with logging and journaling.
func add[In, Out any](h *handlers, s *sdk.Server, t *sdk.Tool, fn sdk.ToolHandlerFor[In, Out]) {
	t.InputSchema = mustSchema[In]()
	t.OutputSchema = mustSchema[Out]()
	sdk.AddTool(s, t, func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, Out, error) {
		start := time.Now()
		res, out, err := fn(ctx, req, in)
		e := journal.Entry{Tool: t.Name, Status: "ok", DurationMS: time.Since(start).Milliseconds()}
		if err != nil {
			e.Status, e.Error = "error", err.Error()
			h.d.Logger.Warn("tool failed", "tool", t.Name, "err", err)
		} else {
			h.d.Logger.Debug("tool done", "tool", t.Name, "ms", e.DurationMS)
		}
		if jerr := h.d.Journal.Record(e); jerr != nil {
			h.d.Logger.Error("journal write failed", "err", jerr)
		}
		return res, out, err
	})
}

// schemaOpts teaches schema inference about types with custom JSON encodings.
var schemaOpts = &jsonschema.ForOptions{
	TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[config.Duration](): {Type: "string", Description: "Go duration, e.g. 120s or 5m"},
	},
}

func mustSchema[T any]() *jsonschema.Schema {
	sch, err := jsonschema.For[T](schemaOpts)
	if err != nil {
		panic(err)
	}
	return sch
}

// errNotImplemented marks tools whose implementation lands in a later stage.
var errNotImplemented = errors.New("not implemented yet")

func notImplemented[In, Out any](name string, stage int) sdk.ToolHandlerFor[In, Out] {
	return func(context.Context, *sdk.CallToolRequest, In) (*sdk.CallToolResult, Out, error) {
		var zero Out
		return nil, zero, fmt.Errorf("%s: %w (planned for stage %d)", name, errNotImplemented, stage)
	}
}

func (h *handlers) stylePath() string {
	return filepath.Join(h.d.ProjectDir, config.DirName, "style.md")
}

func (h *handlers) styleGet(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, StyleOutput, error) {
	b, err := os.ReadFile(h.stylePath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, StyleOutput{Exists: false}, nil
	}
	if err != nil {
		return nil, StyleOutput{}, err
	}
	return nil, StyleOutput{Exists: true, Content: string(b)}, nil
}

func (h *handlers) status(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, StatusOutput, error) {
	c := h.d.Config
	out := StatusOutput{
		Version: h.d.Version,
		Project: h.d.ProjectDir,
		Generator: GeneratorStatus{
			Provider: c.Generator.Provider,
			Model:    c.Generator.Model,
			BaseURL:  c.Generator.BaseURL,
			Slots:    c.Generator.Slots,
			SlotCtx:  c.Generator.SlotCtx,
		},
		Validator: ValidatorStatus{
			Provider: c.Validator.Provider,
			Model:    c.Validator.Model,
			Enabled:  c.Validator.Provider != "none",
		},
		Limits:   c.Limits,
		Graph:    GraphStatus{Provider: c.Graph.Provider},
		Warnings: []string{},
	}
	if h.d.Ops != nil {
		out.Warnings = append(out.Warnings, h.d.Ops.ToolWarnings()...)
	}
	if !out.Validator.Enabled {
		out.Warnings = append(out.Warnings, "validator disabled: the build/lint gate is the only check")
	} else if c.Validator.APIKeyEnv != "" {
		if _, ok := os.LookupEnv(c.Validator.APIKeyEnv); !ok {
			out.Warnings = append(out.Warnings, fmt.Sprintf("validator API key env %s is not set", c.Validator.APIKeyEnv))
		}
	}
	return nil, out, nil
}

func (h *handlers) getSymbol(_ context.Context, _ *sdk.CallToolRequest, in SymbolRef) (*sdk.CallToolResult, GetSymbolOutput, error) {
	info, err := h.d.Ops.GetSymbol(in.File, in.Symbol)
	if err != nil {
		return nil, GetSymbolOutput{}, err
	}
	s := info.Symbol
	return nil, GetSymbolOutput{
		File: info.File, Symbol: s.Name, Kind: s.Kind, Signature: s.Signature,
		Source: info.Source, StartLine: s.StartLine, EndLine: s.EndLine,
	}, nil
}

func (h *handlers) deleteSymbol(ctx context.Context, _ *sdk.CallToolRequest, in DeleteSymbolInput) (*sdk.CallToolResult, ChangeOutput, error) {
	return changeResult(h.d.Ops.DeleteSymbol(ctx, in.File, in.Symbol, in.Force))
}

func (h *handlers) createFile(ctx context.Context, _ *sdk.CallToolRequest, in CreateFileInput) (*sdk.CallToolResult, ChangeOutput, error) {
	return changeResult(h.d.Ops.CreateFile(ctx, in.Path, in.Package))
}

func (h *handlers) deleteFile(ctx context.Context, _ *sdk.CallToolRequest, in DeleteFileInput) (*sdk.CallToolResult, ChangeOutput, error) {
	return changeResult(h.d.Ops.DeleteFile(ctx, in.Path, in.Force))
}

func changeResult(r ops.Result, err error) (*sdk.CallToolResult, ChangeOutput, error) {
	if err != nil {
		return nil, ChangeOutput{}, err
	}
	return nil, ChangeOutput{
		Status: r.Status, Message: r.Message, Diagnostics: r.Diagnostics, Diff: r.Diff, Commit: r.Commit,
		References: r.References, Warnings: r.Warnings,
	}, nil
}

func (h *handlers) check(ctx context.Context, _ *sdk.CallToolRequest, in CheckInput) (*sdk.CallToolResult, CheckOutput, error) {
	rep, err := h.d.Ops.Check(ctx, in.Paths)
	if err != nil {
		return nil, CheckOutput{}, err
	}
	out := CheckOutput{OK: rep.OK, Diagnostics: rep.Diagnostics, Checked: rep.Checked, Warnings: rep.Warnings}
	if out.Diagnostics == nil {
		out.Diagnostics = []Diagnostic{}
	}
	if out.Checked == nil {
		out.Checked = []string{}
	}
	return nil, out, nil
}

func (h *handlers) runTests(ctx context.Context, _ *sdk.CallToolRequest, in RunTestsInput) (*sdk.CallToolResult, TestResult, error) {
	res, err := h.d.Ops.RunTests(ctx, in.Target, in.Filter)
	return nil, res, err
}
