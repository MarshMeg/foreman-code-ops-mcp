package links

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// rawRoute is a route registration as written, before prefixes are applied.
type rawRoute struct {
	offset    int
	recv      string // router variable the route is registered on
	method    string
	path      string
	handler   string // handler expression (identifier, h.Method), if any
	framework string
	decorated bool // the handler is the decorated definition that follows (Python, NestJS)
}

// rawCall is an HTTP client call as written.
type rawCall struct {
	offset int
	method string
	expr   string // URL expression
	client string
	recv   string // client variable, for base URLs
}

// prefixRule says routes on recv (inside [start,end) when scoped) get prefix.
type prefixRule struct {
	recv, parent, prefix string
	start, end           int // 0,0 = whole file
}

// importPrefix applies prefix to all routes of an imported module (app.use("/api", books)).
type importPrefix struct {
	local, prefix string
}

// fileScan is everything detected in one file.
type fileScan struct {
	rel, lang    string
	src          string
	routes       []rawRoute
	calls        []rawCall
	prefixes     []prefixRule
	importPrefix []importPrefix
	imports      map[string]string // local name -> import path (TS, Python)
	consts       map[string]string // identifier -> expression
	clientBase   map[string]string // client variable -> base URL expression
}

var httpMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true}

func methodName(s string) string {
	s = strings.ToUpper(strings.Trim(s, `"' `))
	s = strings.TrimPrefix(s, "HTTP.METHOD")
	if s == "POSTFORM" {
		return "POST"
	}
	if httpMethods[s] {
		return s
	}
	if s == "ALL" || s == "ANY" || s == "" || s == "HANDLE" || s == "HANDLEFUNC" || s == "ROUTE" {
		return AnyMethod
	}
	return s
}

// scan dispatches by adapter language.
func scan(lang, rel, src string) *fileScan {
	fs := &fileScan{rel: rel, lang: lang, src: src, imports: map[string]string{}, consts: map[string]string{}, clientBase: map[string]string{}}
	switch lang {
	case "go":
		scanGo(fs)
	case "python":
		scanPython(fs)
	case "typescript", "tsx", "javascript":
		scanJS(fs)
	}
	sort.SliceStable(fs.routes, func(i, j int) bool { return fs.routes[i].offset < fs.routes[j].offset })
	sort.SliceStable(fs.calls, func(i, j int) bool { return fs.calls[i].offset < fs.calls[j].offset })
	return fs
}

func named(re *regexp.Regexp, m []int, src, name string) string {
	i := re.SubexpIndex(name)
	if i < 0 || m[2*i] < 0 {
		return ""
	}
	return src[m[2*i]:m[2*i+1]]
}

func eachMatch(re *regexp.Regexp, src string, f func(m []int)) {
	for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
		f(m)
	}
}

// ---- Go: net/http (1.22 patterns), chi, gin, echo, fiber; net/http clients.

var (
	goHandle   = regexp.MustCompile(`(?P<recv>\w+)\.(?:HandleFunc|Handle)\(\s*"(?:(?P<method>[A-Z]+)\s+)?(?P<path>/[^"]*)"\s*,\s*(?P<handler>[\w.]+)?`)
	goVerb     = regexp.MustCompile(`(?P<recv>\w+)\.(?P<method>Get|Post|Put|Patch|Delete|Head|Options|GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|Any|All)\(\s*"(?P<path>/[^"]*)"\s*,\s*(?P<handler>[\w.]+)?`)
	goGroup    = regexp.MustCompile(`(?P<recv>\w+)\s*:?=\s*(?P<parent>\w+)\.Group\(\s*"(?P<prefix>/[^"]*)"`)
	goMount    = regexp.MustCompile(`(?P<parent>\w+)\.Mount\(\s*"(?P<prefix>/[^"]*)"\s*,\s*(?P<recv>\w+)\s*\)`)
	goRoute    = regexp.MustCompile(`(?P<parent>\w+)\.Route\(\s*"(?P<prefix>/[^"]*)"\s*,\s*func\(\s*(?P<recv>\w+)`)
	goHTTPCall = regexp.MustCompile(`\bhttp\.(?P<method>Get|Post|Head|PostForm)\(\s*`)
	goNewReq   = regexp.MustCompile(`\bhttp\.NewRequest(?:WithContext)?\(\s*(?:\w+\s*,\s*)?(?P<method>"[A-Z]+"|http\.Method\w+)\s*,\s*`)
	goConst    = regexp.MustCompile(`(?m)^\s*(?:const\s+|var\s+)?(?P<name>\w+)\s*(?::=|=)\s*(?P<expr>.+)$`)
)

func scanGo(fs *fileScan) {
	src := fs.src
	for _, re := range []*regexp.Regexp{goHandle, goVerb} {
		framework := "net/http"
		if re == goVerb {
			framework = "router"
		}
		eachMatch(re, src, func(m []int) {
			method := named(re, m, src, "method")
			fs.routes = append(fs.routes, rawRoute{offset: m[0], recv: named(re, m, src, "recv"), method: methodName(method),
				path: named(re, m, src, "path"), handler: named(re, m, src, "handler"), framework: framework})
		})
	}
	eachMatch(goGroup, src, func(m []int) {
		fs.prefixes = append(fs.prefixes, prefixRule{recv: named(goGroup, m, src, "recv"), parent: named(goGroup, m, src, "parent"), prefix: named(goGroup, m, src, "prefix")})
	})
	eachMatch(goMount, src, func(m []int) {
		fs.prefixes = append(fs.prefixes, prefixRule{recv: named(goMount, m, src, "recv"), parent: named(goMount, m, src, "parent"), prefix: named(goMount, m, src, "prefix")})
	})
	eachMatch(goRoute, src, func(m []int) {
		fs.prefixes = append(fs.prefixes, prefixRule{recv: named(goRoute, m, src, "recv"), parent: named(goRoute, m, src, "parent"),
			prefix: named(goRoute, m, src, "prefix"), start: m[1], end: blockEnd(src, m[1])})
	})
	eachMatch(goHTTPCall, src, func(m []int) {
		fs.calls = append(fs.calls, rawCall{offset: m[0], method: methodName(named(goHTTPCall, m, src, "method")), expr: argument(src, m[1]), client: "net/http"})
	})
	eachMatch(goNewReq, src, func(m []int) {
		fs.calls = append(fs.calls, rawCall{offset: m[0], method: methodName(named(goNewReq, m, src, "method")), expr: argument(src, m[1]), client: "net/http"})
	})
	eachMatch(goConst, src, func(m []int) {
		fs.consts[named(goConst, m, src, "name")] = strings.TrimSpace(named(goConst, m, src, "expr"))
	})
}

// ---- Python: FastAPI, Flask, Django; requests, httpx.

var (
	pyDecorator   = regexp.MustCompile(`(?m)^[ \t]*@(?P<recv>\w+)\.(?P<method>get|post|put|patch|delete|head|options|api_route)\(\s*[rf]?["'](?P<path>[^"']*)["']`)
	pyFlaskRoute  = regexp.MustCompile(`(?m)^[ \t]*@(?P<recv>\w+)\.route\(\s*[rf]?["'](?P<path>[^"']*)["']`)
	pyMethodsArg  = regexp.MustCompile(`methods\s*=\s*\[([^\]]*)\]`)
	pyDjango      = regexp.MustCompile(`\b(?:re_)?path\(\s*r?["'](?P<path>[^"']*)["']\s*,\s*(?P<handler>[\w.]+)`)
	pyRouter      = regexp.MustCompile(`(?P<recv>\w+)\s*=\s*(?:fastapi\.)?APIRouter\((?P<args>[^)]*)\)`)
	pyBlueprint   = regexp.MustCompile(`(?P<recv>\w+)\s*=\s*(?:flask\.)?Blueprint\((?P<args>[^)]*)\)`)
	pyPrefixArg   = regexp.MustCompile(`(?:url_)?prefix\s*=\s*["']([^"']*)["']`)
	pyInclude     = regexp.MustCompile(`\.(?:include_router|register_blueprint)\(\s*(?P<target>[\w.]+)(?P<args>[^)]*)\)`)
	pyImportFrom  = regexp.MustCompile(`(?m)^from\s+(?P<module>[\w.]+)\s+import\s+(?P<names>[\w, ]+)$`)
	pyCall        = regexp.MustCompile(`\b(?P<recv>requests|httpx|\w+)\.(?P<method>get|post|put|patch|delete|head)\(\s*`)
	pyClientVar   = regexp.MustCompile(`(?P<recv>\w+)\s*=\s*(?:httpx\.(?:Async)?Client|requests\.Session)\((?P<args>[^)]*)\)`)
	pyAsyncClient = regexp.MustCompile(`with\s+httpx\.(?:Async)?Client\((?P<args>[^)]*)\)\s+as\s+(?P<recv>\w+)`)
	pyBaseURLArg  = regexp.MustCompile(`base_url\s*=\s*(?P<expr>[^,)]+)`)
	pyConst       = regexp.MustCompile(`(?m)^(?P<name>[A-Za-z_]\w*)\s*(?::\s*\w+\s*)?=\s*(?P<expr>.+)$`)
)

func scanPython(fs *fileScan) {
	src := fs.src
	eachMatch(pyDecorator, src, func(m []int) {
		fs.routes = append(fs.routes, rawRoute{offset: m[0], recv: named(pyDecorator, m, src, "recv"), method: methodName(named(pyDecorator, m, src, "method")),
			path: named(pyDecorator, m, src, "path"), framework: "fastapi/flask", decorated: true})
	})
	eachMatch(pyFlaskRoute, src, func(m []int) {
		methods := []string{AnyMethod}
		if mm := pyMethodsArg.FindStringSubmatch(callTail(src, m[1])); mm != nil {
			methods = nil
			for _, part := range strings.Split(mm[1], ",") {
				if s := methodName(part); s != "" {
					methods = append(methods, s)
				}
			}
		}
		for _, method := range methods {
			fs.routes = append(fs.routes, rawRoute{offset: m[0], recv: named(pyFlaskRoute, m, src, "recv"), method: method,
				path: named(pyFlaskRoute, m, src, "path"), framework: "flask", decorated: true})
		}
	})
	eachMatch(pyDjango, src, func(m []int) {
		fs.routes = append(fs.routes, rawRoute{offset: m[0], method: AnyMethod, path: "/" + strings.TrimLeft(named(pyDjango, m, src, "path"), "^/"),
			handler: named(pyDjango, m, src, "handler"), framework: "django"})
	})
	for _, re := range []*regexp.Regexp{pyRouter, pyBlueprint} {
		eachMatch(re, src, func(m []int) {
			if p := pyPrefixArg.FindStringSubmatch(named(re, m, src, "args")); p != nil {
				fs.prefixes = append(fs.prefixes, prefixRule{recv: named(re, m, src, "recv"), prefix: p[1]})
			}
		})
	}
	eachMatch(pyInclude, src, func(m []int) {
		target := named(pyInclude, m, src, "target")
		p := pyPrefixArg.FindStringSubmatch(named(pyInclude, m, src, "args"))
		if p == nil {
			return
		}
		if mod, _, ok := strings.Cut(target, "."); ok {
			fs.importPrefix = append(fs.importPrefix, importPrefix{local: mod, prefix: p[1]})
		} else {
			fs.prefixes = append(fs.prefixes, prefixRule{recv: target, prefix: p[1]})
			fs.importPrefix = append(fs.importPrefix, importPrefix{local: target, prefix: p[1]})
		}
	})
	eachMatch(pyImportFrom, src, func(m []int) {
		module := named(pyImportFrom, m, src, "module")
		for _, n := range strings.Split(named(pyImportFrom, m, src, "names"), ",") {
			n = strings.TrimSpace(n)
			if n != "" {
				fs.imports[n] = module + "." + n
			}
		}
	})
	eachMatch(pyClientVar, src, func(m []int) {
		if b := pyBaseURLArg.FindStringSubmatch(named(pyClientVar, m, src, "args")); b != nil {
			fs.clientBase[named(pyClientVar, m, src, "recv")] = b[1]
		}
	})
	eachMatch(pyAsyncClient, src, func(m []int) {
		if b := pyBaseURLArg.FindStringSubmatch(named(pyAsyncClient, m, src, "args")); b != nil {
			fs.clientBase[named(pyAsyncClient, m, src, "recv")] = b[1]
		}
	})
	eachMatch(pyCall, src, func(m []int) {
		recv := named(pyCall, m, src, "recv")
		if recv != "requests" && recv != "httpx" {
			if _, ok := fs.clientBase[recv]; !ok && !strings.Contains(src, recv+" = httpx.") && !strings.Contains(src, "as "+recv) &&
				!strings.Contains(src, recv+" = requests.Session") {
				return
			}
		}
		fs.calls = append(fs.calls, rawCall{offset: m[0], method: methodName(named(pyCall, m, src, "method")), expr: argument(src, m[1]), client: "python:" + recv, recv: recv})
	})
	eachMatch(pyConst, src, func(m []int) {
		fs.consts[named(pyConst, m, src, "name")] = strings.TrimSpace(named(pyConst, m, src, "expr"))
	})
}

// ---- TypeScript/JavaScript: express, fastify, hono, NestJS; fetch, axios, ky.

var (
	jsServerVar = regexp.MustCompile(`(?P<recv>\w+)\s*=\s*(?:express\(\)|express\.Router\(\)|Router\(\)|new\s+Hono\b|Fastify\(|fastify\(|new\s+Elysia\b)`)
	jsVerb      = regexp.MustCompile("(?P<recv>\\w+)\\.(?P<method>get|post|put|patch|delete|head|options|all)\\(\\s*[\"'`](?P<path>/[^\"'`]*)[\"'`]\\s*(?:,\\s*(?P<handler>[\\w.]+))?")
	jsUse       = regexp.MustCompile("(?P<parent>\\w+)\\.(?:use|route|register)\\(\\s*[\"'`](?P<prefix>/[^\"'`]*)[\"'`]\\s*,\\s*(?P<recv>\\w+)")
	jsImport    = regexp.MustCompile("import\\s+(?:(?P<def>\\w+)|\\{(?P<names>[^}]*)\\})\\s+from\\s+[\"'](?P<path>[^\"']+)[\"']")
	nestCtrl    = regexp.MustCompile(`@Controller\(\s*(?:["'](?P<prefix>[^"']*)["'])?\s*\)`)
	nestVerb    = regexp.MustCompile(`@(?P<method>Get|Post|Put|Patch|Delete|Head|Options|All)\(\s*(?:["'](?P<path>[^"']*)["'])?\s*\)`)
	jsFetch     = regexp.MustCompile(`\bfetch\(\s*`)
	jsFetchOpt  = regexp.MustCompile(`method\s*:\s*["'](\w+)["']`)
	jsClient    = regexp.MustCompile(`\b(?P<recv>\w+)\.(?P<method>get|post|put|patch|delete|head)(?:<[^>()]*>)?\(\s*`)
	jsClientVar = regexp.MustCompile(`(?P<recv>\w+)\s*=\s*(?:axios|ky)\.(?:create|extend)\(\s*\{(?P<args>[^}]*)\}`)
	jsBaseURL   = regexp.MustCompile(`(?:baseURL|prefixUrl)\s*:\s*(?P<expr>[^,}\n]+)`)
	jsConst     = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:const|let|var)\s+(?P<name>\w+)\s*(?::\s*[\w<>|]+\s*)?=\s*(?P<expr>[^;\n]+)`)
)

func scanJS(fs *fileScan) {
	src := fs.src
	servers := map[string]bool{}
	eachMatch(jsServerVar, src, func(m []int) { servers[named(jsServerVar, m, src, "recv")] = true })
	framework := ""
	for _, f := range []string{"express", "fastify", "hono", "elysia", "koa"} {
		if strings.Contains(src, `"`+f) || strings.Contains(src, `'`+f) {
			framework = f
		}
	}
	if framework != "" {
		for _, common := range []string{"app", "router", "server", "fastify", "api", "routes"} {
			servers[common] = true
		}
	}
	clients := map[string]bool{"axios": true, "ky": true}
	eachMatch(jsClientVar, src, func(m []int) {
		recv := named(jsClientVar, m, src, "recv")
		clients[recv] = true
		if b := jsBaseURL.FindStringSubmatch(named(jsClientVar, m, src, "args")); b != nil {
			fs.clientBase[recv] = strings.TrimSpace(b[1])
		}
	})

	eachMatch(jsVerb, src, func(m []int) {
		recv := named(jsVerb, m, src, "recv")
		if !servers[recv] || clients[recv] {
			return
		}
		fs.routes = append(fs.routes, rawRoute{offset: m[0], recv: recv, method: methodName(named(jsVerb, m, src, "method")),
			path: named(jsVerb, m, src, "path"), handler: named(jsVerb, m, src, "handler"), framework: framework})
	})
	eachMatch(jsUse, src, func(m []int) {
		recv := named(jsUse, m, src, "recv")
		fs.prefixes = append(fs.prefixes, prefixRule{recv: recv, parent: named(jsUse, m, src, "parent"), prefix: named(jsUse, m, src, "prefix")})
		fs.importPrefix = append(fs.importPrefix, importPrefix{local: recv, prefix: named(jsUse, m, src, "prefix")})
	})
	eachMatch(jsImport, src, func(m []int) {
		p := named(jsImport, m, src, "path")
		if d := named(jsImport, m, src, "def"); d != "" {
			fs.imports[d] = p
		}
		for _, n := range strings.Split(named(jsImport, m, src, "names"), ",") {
			n = strings.TrimSpace(n)
			if _, alias, ok := strings.Cut(n, " as "); ok {
				n = strings.TrimSpace(alias)
			}
			if n != "" {
				fs.imports[n] = p
			}
		}
	})
	// NestJS: @Controller("books") prefixes the @Get/@Post decorators that follow it.
	ctrls := nestCtrl.FindAllStringSubmatchIndex(src, -1)
	eachMatch(nestVerb, src, func(m []int) {
		prefix := ""
		for _, c := range ctrls {
			if c[0] < m[0] {
				prefix = named(nestCtrl, c, src, "prefix")
			}
		}
		if len(ctrls) == 0 {
			return
		}
		fs.routes = append(fs.routes, rawRoute{offset: m[0], method: methodName(named(nestVerb, m, src, "method")),
			path: JoinPath("/"+strings.Trim(prefix, "/"), named(nestVerb, m, src, "path")), framework: "nestjs", decorated: true})
	})

	eachMatch(jsFetch, src, func(m []int) {
		method := "GET"
		if o := jsFetchOpt.FindStringSubmatch(callTail(src, m[1])); o != nil {
			method = methodName(o[1])
		}
		fs.calls = append(fs.calls, rawCall{offset: m[0], method: method, expr: argument(src, m[1]), client: "fetch"})
	})
	eachMatch(jsClient, src, func(m []int) {
		recv := named(jsClient, m, src, "recv")
		if !clients[recv] {
			return
		}
		fs.calls = append(fs.calls, rawCall{offset: m[0], method: methodName(named(jsClient, m, src, "method")), expr: argument(src, m[1]), client: recv, recv: recv})
	})
	eachMatch(jsConst, src, func(m []int) {
		fs.consts[named(jsConst, m, src, "name")] = strings.TrimSpace(named(jsConst, m, src, "expr"))
	})
}

// resolveImport maps an import path to a project-relative file candidate set.
func resolveImport(lang, fromRel, imp string) []string {
	switch lang {
	case "python":
		// app.routers.books.router -> app/routers/books.py
		parts := strings.Split(imp, ".")
		var out []string
		for i := len(parts); i > 0; i-- {
			p := strings.Join(parts[:i], "/")
			out = append(out, p+".py", p+"/__init__.py")
		}
		return out
	default:
		if !strings.HasPrefix(imp, ".") {
			return nil
		}
		base := path.Join(path.Dir(fromRel), imp)
		base = strings.TrimSuffix(base, path.Ext(base))
		var out []string
		for _, ext := range []string{".ts", ".tsx", ".js", ".mjs", ".jsx"} {
			out = append(out, base+ext, base+"/index"+ext)
		}
		return out
	}
}
