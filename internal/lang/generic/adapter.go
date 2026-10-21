// Package generic implements lang.Adapter from a YAML spec and tree-sitter queries,
// so adding a language needs no Go code beyond registering its grammar.
package generic

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"text/template"
	"unsafe"

	ts "github.com/tree-sitter/go-tree-sitter"
	"gopkg.in/yaml.v3"

	"github.com/MarshMeg/foreman-code-ops/internal/lang"
)

// Spec is the content of adapters/<lang>/adapter.yaml.
type Spec struct {
	Lang       string   `yaml:"lang"`
	Grammar    string   `yaml:"grammar"`
	Extensions []string `yaml:"extensions"`
	// SymbolsQuery is a .scm file next to adapter.yaml. Each pattern captures the definition
	// as @definition.<kind>, its name as @name and optionally a qualifier as @scope
	// (e.g. a Go method receiver). Definitions nested in other definitions are qualified
	// by their ancestors' names automatically.
	SymbolsQuery string `yaml:"symbols_query"`
	// BodyFields maps a kind to the field holding its body; the signature is the text before it.
	// Kinds not listed use the whole definition as the signature.
	BodyFields map[string]string `yaml:"body_fields"`
	// LeadingComments are node kinds attached to a symbol when directly above it.
	LeadingComments []string `yaml:"leading_comments"`
	// WrapSingle are parent node kinds that become the symbol's range when they hold only
	// this definition (Go: `type X struct{}` is a type_declaration around one type_spec).
	WrapSingle []string `yaml:"wrap_single"`
	NewFile    NewFile  `yaml:"new_file"`
	// Toolchain configures the gate: import insertion, fixers, checks and tests.
	Toolchain lang.Toolchain `yaml:"toolchain"`
	// Prompt is a Markdown file with language guidance for the generator.
	Prompt string `yaml:"prompt"`
	// ImportsQuery is a .scm file capturing import declarations as @import.
	ImportsQuery string `yaml:"imports_query"`
	// Strip lists regular expressions removed from generated code.
	Strip []string `yaml:"strip"`
	// ImportPreamble are top-level node kinds kept above inserted imports when a file has
	// none yet (comment; expression_statement counts only when it is a lone string, i.e. a
	// docstring or a "use client" directive).
	ImportPreamble []string `yaml:"import_preamble"`
	// ImportBare is a template turning a bare module name into an import ("import {}").
	ImportBare string `yaml:"import_bare"`
	// NestedMembers marks languages whose methods live inside the class body.
	NestedMembers bool `yaml:"nested_members"`
}

// NewFile describes new file content.
type NewFile struct {
	// Template is a text/template with .Package available.
	Template string `yaml:"template"`
	// PackageRegexp extracts the package name from a sibling file (first submatch).
	PackageRegexp string `yaml:"package_regexp"`
}

// Adapter is a lang.Adapter driven by a Spec.
type Adapter struct {
	spec     Spec
	language *ts.Language
	query    *ts.Query
	imports  *ts.Query
	prompt   string
	strip    []*regexp.Regexp
	captures []capture
	tmpl     *template.Template
	pkgRe    *regexp.Regexp
}

type captureRole int

const (
	roleOther captureRole = iota
	roleDefinition
	roleName
	roleScope
)

type capture struct {
	role captureRole
	kind string // for roleDefinition
}

// Load reads <dir>/adapter.yaml from fsys. grammars maps grammar names to tree-sitter
// language pointers (as returned by the grammar bindings' Language()).
func Load(fsys fs.FS, dir string, grammars map[string]unsafe.Pointer) (*Adapter, error) {
	raw, err := fs.ReadFile(fsys, path.Join(dir, "adapter.yaml"))
	if err != nil {
		return nil, err
	}
	var spec Spec
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("%s/adapter.yaml: %w", dir, err)
	}
	querySrc, err := fs.ReadFile(fsys, path.Join(dir, spec.SymbolsQuery))
	if err != nil {
		return nil, fmt.Errorf("adapter %s: %w", spec.Lang, err)
	}
	ptr, ok := grammars[spec.Grammar]
	if !ok {
		return nil, fmt.Errorf("adapter %s: grammar %q is not compiled in", spec.Lang, spec.Grammar)
	}
	a, err := New(spec, ts.NewLanguage(ptr), string(querySrc))
	if err != nil {
		return nil, err
	}
	if spec.Prompt != "" {
		b, err := fs.ReadFile(fsys, path.Join(dir, spec.Prompt))
		if err != nil {
			return nil, fmt.Errorf("adapter %s: %w", spec.Lang, err)
		}
		a.prompt = string(b)
	}
	if spec.ImportsQuery != "" {
		b, err := fs.ReadFile(fsys, path.Join(dir, spec.ImportsQuery))
		if err != nil {
			return nil, fmt.Errorf("adapter %s: %w", spec.Lang, err)
		}
		q, qerr := ts.NewQuery(a.language, string(b))
		if qerr != nil {
			return nil, fmt.Errorf("adapter %s: imports query: %s", spec.Lang, qerr.Error())
		}
		a.imports = q
	}
	return a, nil
}

// New builds an adapter from a parsed spec.
func New(spec Spec, language *ts.Language, symbolsQuery string) (*Adapter, error) {
	if spec.Lang == "" || len(spec.Extensions) == 0 {
		return nil, fmt.Errorf("adapter spec needs lang and extensions")
	}
	if err := spec.Toolchain.Validate(); err != nil {
		return nil, fmt.Errorf("adapter %s: toolchain: %w", spec.Lang, err)
	}
	q, qerr := ts.NewQuery(language, symbolsQuery)
	if qerr != nil {
		return nil, fmt.Errorf("adapter %s: symbols query: %s", spec.Lang, qerr.Error())
	}
	a := &Adapter{spec: spec, language: language, query: q}
	for _, expr := range spec.Strip {
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("adapter %s: strip: %w", spec.Lang, err)
		}
		a.strip = append(a.strip, re)
	}
	var hasDef, hasName bool
	for _, name := range q.CaptureNames() {
		c := capture{}
		switch {
		case strings.HasPrefix(name, "definition."):
			c = capture{role: roleDefinition, kind: strings.TrimPrefix(name, "definition.")}
			hasDef = true
		case name == "name":
			c.role, hasName = roleName, true
		case name == "scope":
			c.role = roleScope
		}
		a.captures = append(a.captures, c)
	}
	if !hasDef || !hasName {
		return nil, fmt.Errorf("adapter %s: symbols query must capture @definition.<kind> and @name", spec.Lang)
	}
	tmpl, err := template.New(spec.Lang).Option("missingkey=error").Parse(spec.NewFile.Template)
	if err != nil {
		return nil, fmt.Errorf("adapter %s: new_file.template: %w", spec.Lang, err)
	}
	a.tmpl = tmpl
	if spec.NewFile.PackageRegexp != "" {
		if a.pkgRe, err = regexp.Compile(spec.NewFile.PackageRegexp); err != nil {
			return nil, fmt.Errorf("adapter %s: new_file.package_regexp: %w", spec.Lang, err)
		}
	}
	return a, nil
}

func (a *Adapter) Lang() string              { return a.spec.Lang }
func (a *Adapter) Toolchain() lang.Toolchain { return a.spec.Toolchain }
func (a *Adapter) Extensions() []string      { return a.spec.Extensions }

// Close releases the compiled queries.
func (a *Adapter) Close() {
	a.query.Close()
	if a.imports != nil {
		a.imports.Close()
	}
}

// BareImport implements lang.Adapter.
func (a *Adapter) BareImport() string { return a.spec.ImportBare }

// NestedMembers implements lang.Adapter.
func (a *Adapter) NestedMembers() bool { return a.spec.NestedMembers }

// ImportPoint implements lang.Adapter.
func (a *Adapter) ImportPoint(src []byte) (int, error) {
	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(a.language); err != nil {
		return 0, err
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return 0, fmt.Errorf("%s: parse failed", a.spec.Lang)
	}
	defer tree.Close()
	end := -1
	if a.imports != nil {
		cursor := ts.NewQueryCursor()
		defer cursor.Close()
		matches := cursor.Matches(a.imports, tree.RootNode(), src)
		for m := matches.Next(); m != nil; m = matches.Next() {
			for _, c := range m.Captures {
				if e := int(c.Node.EndByte()); e > end {
					end = e
				}
			}
		}
	}
	if end >= 0 {
		return end, nil
	}
	end = 0
	root := tree.RootNode()
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if !slices.Contains(a.spec.ImportPreamble, n.Kind()) {
			break
		}
		if n.Kind() == "expression_statement" && (n.NamedChildCount() != 1 || n.NamedChild(0).Kind() != "string") {
			break
		}
		end = int(n.EndByte())
	}
	return end, nil
}

// PromptFragment implements lang.Adapter.
func (a *Adapter) PromptFragment() string { return a.prompt }

// Sanitize implements lang.Adapter.
func (a *Adapter) Sanitize(code string) string {
	for _, re := range a.strip {
		code = re.ReplaceAllString(code, "")
	}
	return strings.Trim(code, "\n")
}

// Imports implements lang.Adapter.
func (a *Adapter) Imports(src []byte) ([]string, error) {
	if a.imports == nil {
		return nil, nil
	}
	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(a.language); err != nil {
		return nil, err
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, fmt.Errorf("%s: parse failed", a.spec.Lang)
	}
	defer tree.Close()
	cursor := ts.NewQueryCursor()
	defer cursor.Close()
	var out []string
	matches := cursor.Matches(a.imports, tree.RootNode(), src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		for _, c := range m.Captures {
			if a.imports.CaptureNames()[c.Index] == "import" {
				out = append(out, c.Node.Utf8Text(src))
			}
		}
	}
	return out, nil
}

type rawDef struct {
	pattern uint
	node    *ts.Node
	kind    string
	name    string
	scope   string
}

// Symbols implements lang.Adapter.
func (a *Adapter) Symbols(src []byte) ([]lang.Symbol, error) {
	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(a.language); err != nil {
		return nil, err
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, fmt.Errorf("%s: parse failed", a.spec.Lang)
	}
	defer tree.Close()

	cursor := ts.NewQueryCursor()
	defer cursor.Close()

	// When several patterns match one node, the earliest pattern in the query wins,
	// so more specific patterns go first.
	byID := map[uintptr]*rawDef{}
	var defs []*rawDef
	matches := cursor.Matches(a.query, tree.RootNode(), src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		d := rawDef{pattern: m.PatternIndex}
		for _, c := range m.Captures {
			info := a.captures[c.Index]
			switch info.role {
			case roleDefinition:
				n := c.Node
				d.node, d.kind = &n, info.kind
			case roleName:
				d.name = c.Node.Utf8Text(src)
			case roleScope:
				d.scope = c.Node.Utf8Text(src)
			}
		}
		if d.node == nil || d.name == "" {
			continue
		}
		if prev, seen := byID[d.node.Id()]; seen {
			if d.pattern < prev.pattern {
				*prev = d
			}
			continue
		}
		byID[d.node.Id()] = &d
		defs = append(defs, &d)
	}

	out := make([]lang.Symbol, 0, len(defs))
	for _, d := range defs {
		parts := []string{d.name}
		if d.scope != "" {
			parts = append([]string{d.scope}, parts...)
		}
		for p := d.node.Parent(); p != nil; p = p.Parent() {
			if anc, ok := byID[p.Id()]; ok {
				parts = append([]string{anc.name}, parts...)
			}
		}
		rng := a.outerNode(d)
		start := a.commentStart(rng)
		sym := lang.Symbol{
			Name:      strings.Join(parts, "."),
			Kind:      d.kind,
			Start:     start.start,
			End:       int(rng.EndByte()),
			StartLine: start.row + 1,
			EndLine:   int(rng.EndPosition().Row) + 1,
			Signature: a.signature(d, rng, src),
		}
		out = append(out, sym)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out, nil
}

// outerNode widens a definition to a wrapping declaration that contains only it.
func (a *Adapter) outerNode(d *rawDef) *ts.Node {
	p := d.node.Parent()
	if p == nil || !slices.Contains(a.spec.WrapSingle, p.Kind()) {
		return d.node
	}
	same := 0
	for i := uint(0); i < p.NamedChildCount(); i++ {
		if c := p.NamedChild(i); c != nil && c.Kind() == d.node.Kind() {
			same++
		}
	}
	if same == 1 {
		return p
	}
	return d.node
}

type pos struct{ start, row int }

// commentStart extends n upwards over directly adjacent leading comments.
// A comment that trails code on its own line is not attached.
func (a *Adapter) commentStart(n *ts.Node) pos {
	cur := pos{int(n.StartByte()), int(n.StartPosition().Row)}
	if len(a.spec.LeadingComments) == 0 {
		return cur
	}
	for prev := n.PrevSibling(); prev != nil; prev = prev.PrevSibling() {
		if !slices.Contains(a.spec.LeadingComments, prev.Kind()) {
			break
		}
		if int(prev.EndPosition().Row) != cur.row-1 {
			break
		}
		if before := prev.PrevSibling(); before != nil && before.EndPosition().Row == prev.StartPosition().Row {
			break // trailing comment of the previous statement
		}
		cur = pos{int(prev.StartByte()), int(prev.StartPosition().Row)}
	}
	return cur
}

// signature is the declaration text from the outer node (with export/type keywords) up to
// the body, or the whole declaration for kinds without a body field.
func (a *Adapter) signature(d *rawDef, outer *ts.Node, src []byte) string {
	field, ok := a.spec.BodyFields[d.kind]
	if ok {
		// The body may sit deeper than the definition node: const f = () => { ... }.
		if body := findField(d.node, field); body != nil {
			return strings.TrimSpace(string(src[outer.StartByte():body.StartByte()]))
		}
	}
	return strings.TrimSpace(outer.Utf8Text(src))
}

// findField returns the first node, in pre-order from n, that is the field child of its parent.
func findField(n *ts.Node, field string) *ts.Node {
	if c := n.ChildByFieldName(field); c != nil {
		return c
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c != nil {
			if f := findField(c, field); f != nil {
				return f
			}
		}
	}
	return nil
}

// Find implements lang.Adapter.
func (a *Adapter) Find(src []byte, name string) (lang.Symbol, error) {
	syms, err := a.Symbols(src)
	if err != nil {
		return lang.Symbol{}, err
	}
	var found []lang.Symbol
	for _, s := range syms {
		if s.Name == name {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return lang.Symbol{}, &lang.NotFoundError{Name: name, Candidates: candidates(syms, name)}
	default:
		lines := make([]string, len(found))
		for i, s := range found {
			lines[i] = fmt.Sprint(s.StartLine)
		}
		return lang.Symbol{}, fmt.Errorf("%w: %q is defined %d times (lines %s)",
			lang.ErrAmbiguous, name, len(found), strings.Join(lines, ", "))
	}
}

// candidates suggests names sharing the last segment, or all names in a small file.
func candidates(syms []lang.Symbol, name string) []string {
	last := name[strings.LastIndex(name, ".")+1:]
	var out []string
	for _, s := range syms {
		sl := s.Name[strings.LastIndex(s.Name, ".")+1:]
		if strings.EqualFold(sl, last) {
			out = append(out, s.Name)
		}
	}
	if len(out) == 0 && len(syms) <= 20 {
		for _, s := range syms {
			out = append(out, s.Name)
		}
	}
	return out
}

// NewFile implements lang.Adapter.
func (a *Adapter) NewFile(p string, opts lang.NewFileOpts) ([]byte, error) {
	pkg := opts.Package
	if pkg == "" && a.pkgRe != nil {
		for _, sib := range opts.Siblings {
			if m := a.pkgRe.FindSubmatch(sib); m != nil {
				pkg = string(m[1])
				break
			}
		}
	}
	if pkg == "" {
		pkg = sanitizeIdent(filepath.Base(filepath.Dir(p)))
	}
	var buf bytes.Buffer
	if err := a.tmpl.Execute(&buf, map[string]string{"Package": pkg}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sanitizeIdent(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r == '_' || ('a' <= r && r <= 'z') || ('0' <= r && r <= '9' && b.Len() > 0) {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "main"
	}
	return b.String()
}

var _ lang.Adapter = (*Adapter)(nil)
