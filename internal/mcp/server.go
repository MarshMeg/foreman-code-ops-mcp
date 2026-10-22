// Package mcp exposes foreman-code-ops operations as MCP tools.
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/journal"
	"github.com/MarshMeg/foreman-code-ops/internal/links"
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
			"The generator writes the code; imports are fixed, the file is formatted, linted and built; the validator (if enabled) reviews it, " +
			"and writes it itself when the generator fails. mode=direct lets the validator write it right away (complex logic, algorithms). " +
			"On success the change is committed and the new signature returned; check written_by and reviewed_by, and read the diff when it was not reviewed. " +
			"On failure, diagnostics and the last candidate are returned.",
	}, h.upsertSymbol)

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
		Name: "style_set",
		Description: "Create or update .foreman/style.md (the whole file, or one '## Section' which is replaced or appended) and commit. " +
			"Write it in English. Language sections are named after the language (## Go, ## Python, ## TypeScript); the generator sees the common part and its language's section.",
	}, h.styleSet)

	add(h, s, &sdk.Tool{
		Name: "discover_links",
		Description: "Scan all services for HTTP routes, client calls, OpenAPI/proto contracts and base URLs (docker-compose, .env), " +
			"link calls to the routes they reach, write .foreman/links.json and commit it. Afterwards foreman keeps the file current on every change.",
	}, h.discoverLinks)

	add(h, s, &sdk.Tool{
		Name: "get_links",
		Description: "Cross-service links touching a service, file or symbol, e.g. frontend.useBooks → GET /api/books → backend.BookHandler.List. " +
			"Use it before changing a handler or a client call, and pass the other side as upsert_symbol context.",
	}, h.getLinks)

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
		if j, ok := any(out).(journaled); ok && err == nil {
			j.journal(&e)
		}
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

// journaled outputs add details to their journal entry.
type journaled interface{ journal(*journal.Entry) }

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

func (h *handlers) styleGet(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, StyleOutput, error) {
	info, err := h.d.Ops.StyleGet()
	if err != nil {
		return nil, StyleOutput{}, err
	}
	out := StyleOutput{Exists: info.Exists, Content: info.Content, Sections: info.Sections}
	if out.Sections == nil {
		out.Sections = []string{}
	}
	return nil, out, nil
}

func (h *handlers) styleSet(ctx context.Context, _ *sdk.CallToolRequest, in StyleSetInput) (*sdk.CallToolResult, ChangeOutput, error) {
	return changeResult(h.d.Ops.StyleSet(ctx, in.Section, in.Content))
}

func (h *handlers) status(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, StatusOutput, error) {
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
		slots, slotCtx, running, waiting, warnings := h.d.Ops.GeneratorStatus(ctx)
		out.Generator.Slots, out.Generator.SlotCtx = slots, slotCtx
		out.Queue = QueueStatus{Running: running, Waiting: waiting}
		out.Warnings = append(out.Warnings, warnings...)
		out.Warnings = append(out.Warnings, h.d.Ops.ToolWarnings()...)
		out.Graph.Provider, out.Graph.Available, out.Graph.Reason = h.d.Ops.GraphStatus(ctx)
	}
	if !out.Validator.Enabled {
		out.Warnings = append(out.Warnings, "validator disabled: the build/lint gate is the only check")
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

func (h *handlers) upsertSymbol(ctx context.Context, _ *sdk.CallToolRequest, in UpsertSymbolInput) (*sdk.CallToolResult, UpsertSymbolOutput, error) {
	req := ops.UpsertRequest{
		File: in.File, Symbol: in.Symbol, Kind: in.Kind, Spec: in.Spec, Interface: in.Interface,
		Anchor: in.Anchor, Mode: in.Mode, Package: in.Package, CommitMessage: in.CommitMessage, Limits: in.Limits,
	}
	for _, r := range in.Context {
		req.Context = append(req.Context, ops.Ref{File: r.File, Symbol: r.Symbol})
	}
	switch v := in.RunTests.(type) {
	case nil:
	case bool:
		req.RunTests = v
	case string:
		req.RunTests, req.TestFilter = true, v
	default:
		return nil, UpsertSymbolOutput{}, fmt.Errorf("run_tests must be a boolean or a test filter string, got %T", v)
	}
	r, err := h.d.Ops.Upsert(ctx, req)
	if err != nil {
		return nil, UpsertSymbolOutput{}, err
	}
	out := UpsertSymbolOutput{
		Status: r.Status, Message: r.Message, Symbol: r.Symbol, Signature: r.Signature, Diff: r.Diff, Commit: r.Commit,
		WrittenBy: r.WrittenBy, ReviewedBy: r.ReviewedBy, Attempts: Attempts{BuildFixes: r.BuildFixes, Reviews: r.Reviews},
		MinorNotes: r.MinorNotes, Diagnostics: r.Diagnostics, LastCode: r.LastCode, Warnings: r.Warnings,
		file: req.File,
	}
	if out.Diagnostics == nil {
		out.Diagnostics = []Diagnostic{}
	}
	if out.MinorNotes == nil {
		out.MinorNotes = []string{}
	}
	if r.Tests != nil {
		out.Tests = *r.Tests
	}
	return nil, out, nil
}

func (o UpsertSymbolOutput) journal(e *journal.Entry) {
	e.File, e.Symbol, e.Status, e.WrittenBy = o.file, o.Symbol, o.Status, o.WrittenBy
	e.BuildFixes, e.Reviews, e.Commit = o.Attempts.BuildFixes, o.Attempts.Reviews, o.Commit
}

func (h *handlers) discoverLinks(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, DiscoverLinksOutput, error) {
	r, err := h.d.Ops.DiscoverLinks(ctx)
	if err != nil {
		return nil, DiscoverLinksOutput{}, err
	}
	out := DiscoverLinksOutput{Status: r.Status, Message: r.Message, Commit: r.Commit, Services: r.Map.Services(),
		Routes: len(r.Map.Routes), Calls: len(r.Map.Calls), Links: []string{}}
	linked := map[string]bool{}
	for _, l := range r.Map.Links {
		out.Links = append(out.Links, l.String())
		linked[fmt.Sprintf("%s:%d", l.From.File, l.From.Line)] = true
	}
	for _, c := range r.Map.Calls {
		if !linked[fmt.Sprintf("%s:%d", c.File, c.Line)] {
			out.Unlinked = append(out.Unlinked, fmt.Sprintf("%s:%d %s %s (%s)", c.File, c.Line, c.Method, c.Path, c.Client))
		}
	}
	if out.Services == nil {
		out.Services = []string{}
	}
	return nil, out, nil
}

func (h *handlers) getLinks(_ context.Context, _ *sdk.CallToolRequest, in GetLinksInput) (*sdk.CallToolResult, GetLinksOutput, error) {
	ls, err := h.d.Ops.GetLinks(in.Service, in.File, in.Symbol)
	if err != nil {
		return nil, GetLinksOutput{}, err
	}
	if ls == nil {
		ls = []links.Link{}
	}
	return nil, GetLinksOutput{Links: ls}, nil
}
