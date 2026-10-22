package links

import (
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang/builtin"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/project"
	tu "github.com/MarshMeg/foreman-code-ops-mcp/internal/testutil"
)

func discover(t *testing.T, dir string) *Map {
	t.Helper()
	proj, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := builtin.Registry()
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(proj, reg).Discover(nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func linkLines(m *Map) string {
	var out []string
	for _, l := range m.Links {
		out = append(out, l.String())
	}
	return strings.Join(out, "\n")
}

func TestMonorepo(t *testing.T) {
	dir := tu.RepoFromFixture(t, "../../testdata/monorepo")
	m := discover(t, dir)

	var routes []string
	for _, r := range m.Routes {
		routes = append(routes, r.Method+" "+r.Path+" "+r.Service+"."+r.Symbol)
		if !r.Contract {
			t.Errorf("route %s %s not covered by openapi", r.Method, r.Path)
		}
	}
	want := "GET /api/books backend.BookHandler.List;GET /api/books/{} backend.BookHandler.Get;POST /api/books backend.BookHandler.Create"
	if got := strings.Join(routes, ";"); got != want {
		t.Fatalf("routes:\n got %s\nwant %s", got, want)
	}

	got := linkLines(m)
	for _, want := range []string{
		"frontend.createBook → POST /api/books → backend.BookHandler.Create [contract]",
		"frontend.useBooks → GET /api/books → backend.BookHandler.List [contract]",
		"worker.fetch_book → GET /api/books/{} → backend.BookHandler.Get [contract]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("links lack %q:\n%s", want, got)
		}
	}
	if len(m.Links) != 3 {
		t.Errorf("links:\n%s", got)
	}
	for _, c := range m.Calls {
		if c.Target != "backend" {
			t.Errorf("call %+v: target not resolved", c)
		}
	}

	if q := m.Query("", "", "useBooks"); len(q) != 1 {
		t.Errorf("query useBooks = %v", q)
	}
	if c := m.Callers("backend/handler.go", "BookHandler.List"); len(c) != 1 || c[0].From.Symbol != "useBooks" {
		t.Errorf("callers = %v", c)
	}
	// Deterministic output.
	if string(m.Marshal()) != string(discover(t, dir).Marshal()) {
		t.Error("output is not deterministic")
	}
}

func TestNormalizePath(t *testing.T) {
	for in, want := range map[string]string{
		"/books/{id}":                "/books/{}",
		"/books/:id/":                "/books/{}",
		"books/<int:pk>/":            "/books/{}",
		"/books/${id}?x=1":           "/books/{}",
		"http://backend:8080/api/x/": "/api/x",
		"/files/*path":               "/files/{}",
		"/":                          "/",
		"//a//b":                     "/a/b",
		"/books/%d":                  "/books/{}",
	} {
		if got := NormalizePath(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestURLExpr(t *testing.T) {
	cases := []struct{ expr, tmpl, base string }{
		{`baseURL + "/books/" + id`, "{}/books/{}", "baseURL"},
		{"`${API}/books/${id}`", "{}/books/{}", "API"},
		{`f"{BASE}/books/{book_id}"`, "{}/books/{}", "BASE"},
		{`fmt.Sprintf("%s/books/%d", base, id)`, "%s/books/%d", "base"},
		{`"/health"`, "/health", ""},
	}
	for _, c := range cases {
		tmpl, base, _ := urlExpr(c.expr)
		if tmpl != c.tmpl || base != c.base {
			t.Errorf("%s: got %q %q", c.expr, tmpl, base)
		}
	}
}

func routesOf(lang, src string) []string {
	fs := scan(lang, "f", src)
	var out []string
	for _, r := range fs.routes {
		out = append(out, r.method+" "+NormalizePath(JoinPath(fs.prefixFor(r.recv, r.offset, 0), r.path)))
	}
	return out
}

func TestGoRouters(t *testing.T) {
	src := `
func routes() {
	r := chi.NewRouter()
	r.Get("/health", health)
	r.Route("/api", func(api chi.Router) {
		api.Get("/books/{id}", h.Get)
		api.Post("/books", h.Create)
	})
	g := e.Group("/v2")
	g.GET("/books", h.List)
	sub := chi.NewRouter()
	sub.Delete("/books/{id}", h.Delete)
	r.Mount("/admin", sub)
	mux.Handle("/static/", files)
}`
	got := strings.Join(routesOf("go", src), "; ")
	want := "GET /health; GET /api/books/{}; POST /api/books; GET /v2/books; DELETE /admin/books/{}; ANY /static"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestPythonRouters(t *testing.T) {
	src := `
router = APIRouter(prefix="/books")

@router.get("/{book_id}")
def get_book(book_id: int): ...

@app.route("/legacy", methods=["GET", "POST"])
def legacy(): ...

urlpatterns = [path("authors/<int:pk>/", views.author)]
`
	got := strings.Join(routesOf("python", src), "; ")
	if got != "GET /books/{}; GET /legacy; POST /legacy; ANY /authors/{}" {
		t.Errorf("got %s", got)
	}
}

func TestJSRouters(t *testing.T) {
	express := `
import express from "express";
const app = express();
const router = express.Router();
router.get("/books/:id", getBook);
app.post("/books", createBook);
app.use("/api", router);
axios.get("/not-a-route");
`
	if got := strings.Join(routesOf("typescript", express), "; "); got != "GET /api/books/{}; POST /books" {
		t.Errorf("express: %s", got)
	}
	nest := `
@Controller("books")
export class BooksController {
  @Get(":id")
  find() {}
  @Post()
  create() {}
}`
	if got := strings.Join(routesOf("typescript", nest), "; "); got != "GET /books/{}; POST /books" {
		t.Errorf("nest: %q", got)
	}
}

func TestClients(t *testing.T) {
	ts := scan("typescript", "f.ts", "const api = axios.create({ baseURL: process.env.API_URL });\nawait api.delete(`/books/${id}`);\nawait ky.get(\"/x\");\nawait fetch(\"/books\", { method: \"PUT\", body });\nmap.get(\"/nope\");\n")
	var got []string
	for _, c := range ts.calls {
		call, ok := ts.resolveCall(c, &infra{envTarget: map[string]string{"API_URL": "backend"}, envPath: map[string]string{}, hostTarget: map[string]string{}})
		if ok {
			got = append(got, call.Method+" "+call.Path+" "+call.Target)
		}
	}
	if strings.Join(got, "; ") != "DELETE /books/{} backend; GET /x ; PUT /books " {
		t.Errorf("ts calls: %q", got)
	}
	gosrc := scan("go", "f.go", `req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/books", body)`+"\n"+`resp, _ := http.Get(fmt.Sprintf("%s/books/%d", base, id))`)
	got = nil
	for _, c := range gosrc.calls {
		call, _ := gosrc.resolveCall(c, &infra{envTarget: map[string]string{}, envPath: map[string]string{}, hostTarget: map[string]string{}})
		got = append(got, call.Method+" "+call.Path)
	}
	if strings.Join(got, "; ") != "POST /books; GET /books/{}" {
		t.Errorf("go calls: %q", got)
	}
}

func TestOpenAPI(t *testing.T) {
	ops := parseOpenAPI([]byte("swagger: '2.0'\nbasePath: /v1\npaths:\n  /books/{id}:\n    get: {}\n    parameters: []\n"))
	if !ops["GET /v1/books/{}"] || len(ops) != 1 {
		t.Errorf("ops = %v", ops)
	}
}

func TestGRPC(t *testing.T) {
	tu.IsolateGit(t)
	dir := t.TempDir()
	tu.WriteFile(t, dir, ".foreman/workspace.yaml", "services:\n  - {name: catalog, root: catalog, lang: go}\n  - {name: web, root: web, lang: go}\n")
	tu.WriteFile(t, dir, "catalog/go.mod", "module catalog\n")
	tu.WriteFile(t, dir, "catalog/proto/books.proto", "syntax = \"proto3\";\npackage books.v1;\n\nservice BookService {\n  rpc ListBooks(ListRequest) returns (ListResponse);\n  rpc GetBook(GetRequest) returns (Book);\n}\n")
	tu.WriteFile(t, dir, "web/go.mod", "module web\n")
	tu.WriteFile(t, dir, "web/client.go", "package web\n\n// Titles lists titles.\nfunc Titles(c booksv1.BookServiceClient) {\n\tc.ListBooks(ctx, &booksv1.ListRequest{})\n}\n")
	m := discover(t, dir)
	if len(m.Links) != 1 {
		t.Fatalf("links:\n%s", linkLines(m))
	}
	if got := m.Links[0].String(); got != "web.Titles → GRPC /books.v1.BookService/ListBooks → catalog.BookService.ListBooks [contract]" {
		t.Fatalf("link = %s", got)
	}
}
