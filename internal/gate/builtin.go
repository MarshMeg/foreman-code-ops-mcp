package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"strconv"
	"strings"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/imports"
)

// fileJob is one file passed through a built-in step.
type fileJob struct {
	abs, rel string
	src      []byte
	imports  []string // insert-imports only
	adapter  lang.Adapter
}

// builtinFunc rewrites a file. Problems in the code are returned as diagnostics;
// the error is reserved for infrastructure failures.
type builtinFunc func(ctx context.Context, job fileJob, source string) ([]byte, []Diagnostic, error)

var builtins = map[string]builtinFunc{
	"goimports":         goimportsStep,
	"gofmt":             gofmtStep,
	"go-insert-imports": goInsertImports,
	"insert-imports":    textInsertImports,
}

// textInsertImports adds import lines that are not in the file yet at the adapter's import
// point. Formatters and import organizers (ruff, biome) sort and merge them afterwards.
func textInsertImports(_ context.Context, job fileJob, source string) ([]byte, []Diagnostic, error) {
	a := job.adapter
	existing, err := a.Imports(job.src)
	if err != nil {
		return nil, nil, err
	}
	have := map[string]bool{}
	for _, imp := range existing {
		for _, l := range strings.Split(imp, "\n") {
			have[strings.TrimSpace(l)] = true
		}
	}
	var lines []string
	var diags []Diagnostic
	for _, raw := range job.imports {
		imp := strings.TrimSpace(raw)
		if imp == "" {
			continue
		}
		if !strings.HasPrefix(imp, "import ") && !strings.HasPrefix(imp, "from ") {
			if a.BareImport() == "" {
				diags = append(diags, Diagnostic{File: job.rel, Severity: SeverityWarning, Source: source,
					Message: fmt.Sprintf("ignored import %q: write a full import statement", imp)})
				continue
			}
			imp = strings.ReplaceAll(a.BareImport(), "{}", strings.Trim(imp, "\"'"))
		}
		if have[imp] {
			continue
		}
		have[imp] = true
		lines = append(lines, imp)
	}
	if len(lines) == 0 {
		return job.src, diags, nil
	}
	at, err := a.ImportPoint(job.src)
	if err != nil {
		return nil, nil, err
	}
	block := strings.Join(lines, "\n")
	var out []byte
	if at == 0 {
		out = append([]byte(block+"\n\n"), job.src...)
	} else {
		out = append(append(append([]byte{}, job.src[:at]...), []byte("\n"+block)...), job.src[at:]...)
	}
	return out, diags, nil
}

func goimportsStep(_ context.Context, job fileJob, source string) ([]byte, []Diagnostic, error) {
	out, err := imports.Process(job.abs, job.src, &imports.Options{Comments: true, TabIndent: true, TabWidth: 8})
	if err != nil {
		return job.src, goSyntaxDiagnostics(err, job.rel, source), nil
	}
	return out, nil, nil
}

func gofmtStep(_ context.Context, job fileJob, source string) ([]byte, []Diagnostic, error) {
	out, err := format.Source(job.src)
	if err != nil {
		return job.src, goSyntaxDiagnostics(err, job.rel, source), nil
	}
	return out, nil, nil
}

// goInsertImports adds import specs given as `"path"`, `path`, `name "path"` or
// `import name "path"`. Existing imports are left alone; goimports removes unused ones later.
func goInsertImports(_ context.Context, job fileJob, source string) ([]byte, []Diagnostic, error) {
	if len(job.imports) == 0 {
		return job.src, nil, nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, job.rel, job.src, parser.ParseComments)
	if err != nil {
		return job.src, goSyntaxDiagnostics(err, job.rel, source), nil
	}
	var diags []Diagnostic
	for _, raw := range job.imports {
		name, path, err := parseGoImport(raw)
		if err != nil {
			diags = append(diags, Diagnostic{File: job.rel, Severity: SeverityError, Source: source, Message: err.Error()})
			continue
		}
		astutil.AddNamedImport(fset, f, name, path)
	}
	ast.SortImports(fset, f)
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, nil, fmt.Errorf("print %s: %w", job.rel, err)
	}
	return buf.Bytes(), diags, nil
}

func parseGoImport(raw string) (name, path string, err error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSpace(strings.TrimPrefix(s, "import "))
	if i := strings.IndexAny(s, "\"`"); i > 0 {
		name, s = strings.TrimSpace(s[:i]), s[i:]
	}
	if strings.HasPrefix(s, "\"") || strings.HasPrefix(s, "`") {
		if path, err = strconv.Unquote(s); err != nil {
			return "", "", fmt.Errorf("invalid import %q", raw)
		}
	} else {
		path = s
	}
	if path == "" || strings.ContainsAny(path, " \t\n\"") {
		return "", "", fmt.Errorf("invalid import %q", raw)
	}
	return name, path, nil
}

// goSyntaxDiagnostics converts go/scanner errors ("file:line:col: msg") into diagnostics.
func goSyntaxDiagnostics(err error, rel, source string) []Diagnostic {
	var list scanner.ErrorList
	if errors.As(err, &list) {
		out := make([]Diagnostic, 0, len(list))
		for _, e := range list {
			out = append(out, Diagnostic{File: rel, Line: e.Pos.Line, Column: e.Pos.Column, Severity: SeverityError, Source: source, Message: e.Msg})
		}
		return out
	}
	var one *scanner.Error
	if errors.As(err, &one) {
		return []Diagnostic{{File: rel, Line: one.Pos.Line, Column: one.Pos.Column, Severity: SeverityError, Source: source, Message: one.Msg}}
	}
	return []Diagnostic{{File: rel, Severity: SeverityError, Source: source, Message: err.Error()}}
}
