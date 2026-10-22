package links

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/project"
)

// Source provides file contents; nil content means "read from disk". It lets the caller
// recompute links for candidate files before they are committed.
type Source func(rel string) ([]byte, bool, error)

// Discoverer builds the link map of a project.
type Discoverer struct {
	proj *project.Project
	reg  *lang.Registry
}

// New creates a Discoverer.
func New(proj *project.Project, reg *lang.Registry) *Discoverer {
	return &Discoverer{proj: proj, reg: reg}
}

// services returns the declared services, or the project root as one service.
func (d *Discoverer) services() []project.Service {
	if len(d.proj.Workspace.Services) > 0 {
		return d.proj.Workspace.Services
	}
	return []project.Service{{Name: filepath.Base(d.proj.Root), Root: "."}}
}

var testFile = regexp.MustCompile(`(_test\.go|^test_.*\.py|_test\.py|\.(test|spec)\.[cm]?[jt]sx?)$`)

// Discover scans every service. src may override file contents (nil: disk).
func (d *Discoverer) Discover(src Source) (*Map, error) {
	svcs := d.services()
	var scans []*fileScan
	owner := map[string]string{} // file -> service
	for _, s := range svcs {
		root := filepath.Join(d.proj.Root, filepath.FromSlash(s.Root))
		err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if e.IsDir() {
				if p != root && skipDir(e.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(d.proj.Root, p)
			rel = filepath.ToSlash(rel)
			if svc := d.proj.ServiceFor(rel); len(d.proj.Workspace.Services) > 0 && (svc == nil || svc.Name != s.Name) {
				return nil // belongs to a nested service
			}
			a := d.reg.ForFile(rel)
			if a == nil || testFile.MatchString(path.Base(rel)) {
				return nil
			}
			content, err := read(src, d.proj.Root, rel)
			if err != nil || content == nil {
				return nil
			}
			scans = append(scans, scan(a.Lang(), rel, string(content)))
			owner[rel] = s.Name
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return d.build(svcs, scans, owner), nil
}

func read(src Source, root, rel string) ([]byte, error) {
	if src != nil {
		b, exists, err := src(rel)
		if err != nil || !exists {
			return nil, err
		}
		return b, nil
	}
	return os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
}

type symIndex struct {
	byFile map[string][]lang.Symbol
	byName map[string][]Endpoint // last name segment -> definitions (service, file, symbol)
}

func (d *Discoverer) build(svcs []project.Service, scans []*fileScan, owner map[string]string) *Map {
	in := loadInfra(d.proj.Root, svcs)
	idx := symIndex{byFile: map[string][]lang.Symbol{}, byName: map[string][]Endpoint{}}
	byRel := map[string]*fileScan{}
	for _, fs := range scans {
		byRel[fs.rel] = fs
		if a := d.reg.ForFile(fs.rel); a != nil {
			if syms, err := a.Symbols([]byte(fs.src)); err == nil {
				idx.byFile[fs.rel] = syms
				for _, s := range syms {
					last := s.Name[strings.LastIndex(s.Name, ".")+1:]
					idx.byName[last] = append(idx.byName[last], Endpoint{Service: owner[fs.rel], File: fs.rel, Symbol: s.Name})
				}
			}
		}
	}
	// Prefixes that other files apply to a file's routes (app.use("/api", books)).
	filePrefix := map[string]string{}
	for _, fs := range scans {
		for _, ip := range fs.importPrefix {
			imp, ok := fs.imports[ip.local]
			if !ok {
				continue
			}
			for _, cand := range resolveImport(fs.lang, fs.rel, imp) {
				for _, rootCand := range []string{cand, path.Join(path.Dir(fs.rel), cand)} {
					if _, ok := byRel[rootCand]; ok {
						filePrefix[rootCand] = JoinPath(ip.prefix, filePrefix[rootCand])
					}
				}
			}
			// Python: "from app.routers import books" names a module, possibly relative to the service root.
			if fs.lang == "python" {
				for rel := range byRel {
					if strings.HasSuffix(rel, strings.ReplaceAll(imp, ".", "/")+".py") {
						filePrefix[rel] = JoinPath(ip.prefix, filePrefix[rel])
					}
				}
			}
		}
	}

	m := &Map{Version: 1}
	for _, fs := range scans {
		svc := owner[fs.rel]
		for _, r := range fs.routes {
			full := JoinPath(filePrefix[fs.rel], JoinPath(fs.prefixFor(r.recv, r.offset, 0), r.path))
			route := Route{Endpoint: Endpoint{Service: svc, File: fs.rel, Line: lineOf(fs.src, r.offset), Method: r.method, Path: NormalizePath(full)}, Framework: r.framework}
			route.File, route.Symbol = d.handler(fs, r, idx, svc)
			if route.File != fs.rel {
				route.Line = d.symbolLine(idx, route.File, route.Symbol, route.Line)
			}
			route.Contract = in.ops[svc][route.Method+" "+route.Path]
			m.Routes = append(m.Routes, route)
		}
		for _, c := range fs.calls {
			call, ok := fs.resolveCall(c, in)
			if !ok {
				continue
			}
			call.Service, call.File, call.Line = svc, fs.rel, lineOf(fs.src, c.offset)
			call.Symbol = enclosing(idx.byFile[fs.rel], c.offset)
			m.Calls = append(m.Calls, call)
		}
	}
	m.addGRPC(in, scans, owner, idx)
	m.match(in)
	m.sort()
	return m
}

// prefixFor composes the router prefixes that apply to recv at offset.
func (fs *fileScan) prefixFor(recv string, offset, depth int) string {
	if recv == "" || depth > 8 {
		return ""
	}
	best := -1
	for i, p := range fs.prefixes {
		if p.recv != recv {
			continue
		}
		if p.end > 0 && (offset < p.start || offset > p.end) {
			continue
		}
		// Prefer the innermost scoped rule over a whole-file one.
		if best < 0 || (p.end > 0 && (fs.prefixes[best].end == 0 || p.start > fs.prefixes[best].start)) {
			best = i
		}
	}
	if best < 0 {
		return ""
	}
	p := fs.prefixes[best]
	parent := ""
	if p.parent != "" && p.parent != recv {
		parent = fs.prefixFor(p.parent, offset, depth+1)
	}
	return JoinPath(parent, p.prefix)
}

// handler resolves the route's handler to a symbol: the decorated definition, the handler
// argument (an identifier or method value), or the enclosing symbol.
func (d *Discoverer) handler(fs *fileScan, r rawRoute, idx symIndex, svc string) (string, string) {
	syms := idx.byFile[fs.rel]
	if r.decorated {
		for _, s := range syms {
			if s.Start >= r.offset {
				return fs.rel, s.Name
			}
		}
	}
	if r.handler != "" {
		name := r.handler[strings.LastIndex(r.handler, ".")+1:]
		var cands []Endpoint
		for _, e := range idx.byName[name] {
			if e.Service == svc {
				cands = append(cands, e)
			}
		}
		for _, e := range cands {
			if e.File == fs.rel {
				return e.File, e.Symbol
			}
		}
		if len(cands) == 1 {
			return cands[0].File, cands[0].Symbol
		}
	}
	return fs.rel, enclosing(syms, r.offset)
}

func (d *Discoverer) symbolLine(idx symIndex, file, symbol string, fallback int) int {
	for _, s := range idx.byFile[file] {
		if s.Name == symbol {
			return s.StartLine
		}
	}
	return fallback
}

// enclosing returns the innermost symbol containing offset.
func enclosing(syms []lang.Symbol, offset int) string {
	name, size := "", -1
	for _, s := range syms {
		if s.Start <= offset && offset < s.End && (size < 0 || s.End-s.Start < size) {
			name, size = s.Name, s.End-s.Start
		}
	}
	return name
}

// resolveCall turns a raw call into a Call with a path and, when known, a target service.
func (fs *fileScan) resolveCall(c rawCall, in *infra) (Call, bool) {
	expr := c.expr
	if v, ok := fs.consts[expr]; ok {
		expr = v
	}
	template, base, dynamic := urlExpr(expr)
	if b, ok := fs.clientBase[c.recv]; ok && base == "" {
		base = b
	}
	if v, ok := fs.consts[base]; ok {
		base = v
	}
	raw := template
	if strings.Contains(template, "://") {
		u := template[strings.Index(template, "://")+3:]
		host := u
		if i := strings.IndexAny(u, "/:"); i >= 0 {
			host = u[:i]
		}
		call := Call{Client: c.client, Raw: raw, Dynamic: dynamic, Target: in.hostTarget[host]}
		call.Method, call.Path = c.method, NormalizePath(template)
		return call, true
	}
	p := stripBase(template)
	if !strings.HasPrefix(p, "/") {
		return Call{}, false
	}
	call := Call{Client: c.client, Raw: raw, Dynamic: dynamic}
	if v := envVar(base); v != "" {
		call.Target = in.envTarget[v]
		p = JoinPath(in.envPath[v], p)
	}
	call.Method, call.Path = c.method, NormalizePath(p)
	return call, true
}

var grpcCall = regexp.MustCompile(`\.(\w+)\(`)

// addGRPC adds protobuf services as routes and calls to their RPCs from files that use the
// generated client (they mention <Service>Client).
func (m *Map) addGRPC(in *infra, scans []*fileScan, owner map[string]string, idx symIndex) {
	for _, g := range in.grpc {
		full := g.name
		if g.pkg != "" {
			full = g.pkg + "." + g.name
		}
		for _, rpc := range g.rpcs {
			m.Routes = append(m.Routes, Route{Endpoint: Endpoint{Service: g.owner, File: g.file, Method: "GRPC", Path: "/" + full + "/" + rpc,
				Symbol: g.name + "." + rpc}, Framework: "grpc", Contract: true})
		}
		for _, fs := range scans {
			if owner[fs.rel] == g.owner || !strings.Contains(fs.src, g.name+"Client") {
				continue
			}
			for _, loc := range grpcCall.FindAllStringSubmatchIndex(fs.src, -1) {
				name := fs.src[loc[2]:loc[3]]
				for _, rpc := range g.rpcs {
					if name == rpc {
						m.Calls = append(m.Calls, Call{Endpoint: Endpoint{Service: owner[fs.rel], File: fs.rel, Line: lineOf(fs.src, loc[0]),
							Symbol: enclosing(idx.byFile[fs.rel], loc[0]), Method: "GRPC", Path: "/" + full + "/" + rpc}, Client: "grpc", Target: g.owner})
					}
				}
			}
		}
	}
}

// match links calls to routes of other services.
func (m *Map) match(in *infra) {
	for _, c := range m.Calls {
		var exact, suffix []Route
		for _, r := range m.Routes {
			if r.Service == c.Service || (c.Target != "" && r.Service != c.Target) {
				continue
			}
			if c.Method != r.Method && c.Method != AnyMethod && r.Method != AnyMethod {
				continue
			}
			switch {
			case c.Path == r.Path:
				exact = append(exact, r)
			case len(r.Path) > 1 && strings.HasSuffix(c.Path, r.Path) && c.Path[len(c.Path)-len(r.Path)-1] != '{':
				suffix = append(suffix, r)
			}
		}
		add := func(rs []Route, conf string) {
			for _, r := range rs {
				l := Link{From: c.Endpoint, To: r.Endpoint, Confidence: conf}
				switch {
				case conf == Matched && r.Contract:
					l.Confidence = Contract
				case conf == Matched && (len(rs) > 1 && c.Target == "" || c.Dynamic && strings.Count(c.Path, "{}") > strings.Count(r.Path, "{}")):
					l.Confidence = Guessed
				}
				m.Links = append(m.Links, l)
			}
		}
		if len(exact) > 0 {
			add(exact, Matched)
		} else {
			add(suffix, Guessed)
		}
	}
}

// Services returns the sorted names of services with routes or calls.
func (m *Map) Services() []string {
	seen := map[string]bool{}
	for _, r := range m.Routes {
		seen[r.Service] = true
	}
	for _, c := range m.Calls {
		seen[c.Service] = true
	}
	var out []string
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
