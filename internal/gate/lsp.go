package gate

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lsp"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/workspace"
)

// defaultSettle is the wait for further pushed diagnostics (servers without pull diagnostics).
const defaultSettle = 500 * time.Millisecond

// SetLSP enables language servers as a first check stage; nil disables them.
func (g *Gate) SetLSP(p *lsp.Pool) { g.lsp = p }

// lspCheck diagnoses the group's changed files with the first applicable language server.
// It returns the step name and the diagnostics, or ok=false when no server was usable (the
// check commands then run as usual). Only diagnostics in the changed files are reported:
// errors elsewhere caused by the change are left to the check commands.
func (g *Gate) lspCheck(ctx context.Context, gr *group, cs *workspace.Changeset) (name string, ds []Diagnostic, warnings []string, ok bool) {
	tc := gr.adapter.Toolchain()
	if g.lsp == nil || len(tc.LSP) == 0 || cs == nil {
		return "", nil, nil, false
	}
	changed := cs.Files()
	var files []string
	for _, f := range gr.files {
		if slices.Contains(changed, f) {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return "", nil, nil, false
	}
	e := g.plainEnv(gr.adapter, gr.root)
	for _, srv := range tc.LSP {
		if !e.applies(srv.When) {
			continue
		}
		bin, found := e.resolve(srv.Run[0])
		if !found {
			continue
		}
		docs, usable := lspDocs(g.proj.Root, files, cs, srv)
		if !usable {
			continue
		}
		settle := defaultSettle
		if srv.SettleMS > 0 {
			settle = time.Duration(srv.SettleMS) * time.Millisecond
		}
		lctx, cancel := context.WithTimeout(ctx, g.limits.CheckTimeout.D())
		argv := append([]string{bin}, srv.Run[1:]...)
		got, err := g.lsp.Diagnose(lctx, lsp.Server{Argv: argv, Dir: e.realDir, Env: os.Environ(), InitOptions: srv.InitOptions}, docs, settle)
		cancel()
		if err != nil {
			warnings = append(warnings, "lsp "+srv.Name+" skipped: "+firstLine(err.Error()))
			continue
		}
		source := srv.Name + " (lsp)"
		for _, d := range got {
			if d.Severity > 2 {
				continue // information and hints
			}
			rel, err := filepath.Rel(g.proj.Root, d.File)
			if err != nil {
				continue
			}
			sev := SeverityError
			if d.Severity == 2 {
				sev = SeverityWarning
			}
			msg := d.Message
			if d.Code != "" {
				msg += " [" + d.Code + "]"
			}
			ds = append(ds, Diagnostic{File: filepath.ToSlash(rel), Line: d.Line, Column: d.Col, Severity: sev, Source: source, Message: msg})
		}
		return source, ds, warnings, true
	}
	return "", nil, warnings, false
}

// lspDocs reads the candidate content of files; usable is false when one has no language id.
func lspDocs(root string, files []string, cs *workspace.Changeset, srv lang.LSPServer) ([]lsp.Document, bool) {
	var docs []lsp.Document
	for _, f := range files {
		id := srv.LanguageIDs[strings.ToLower(path.Ext(f))]
		if id == "" {
			return nil, false
		}
		src, exists, err := cs.Read(f)
		if err != nil || !exists {
			continue
		}
		docs = append(docs, lsp.Document{Path: filepath.Join(root, filepath.FromSlash(f)), LanguageID: id, Text: src})
	}
	return docs, len(docs) > 0
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
