package builtin

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops/internal/lang"
)

func adapterFor(t *testing.T, file string) lang.Adapter {
	t.Helper()
	r, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	a := r.ForFile(file)
	if a == nil {
		t.Fatalf("no adapter for %s", file)
	}
	return a
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../../testdata", path))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func symbolList(t *testing.T, a lang.Adapter, src []byte) string {
	t.Helper()
	syms, err := a.Symbols(src)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range syms {
		out = append(out, s.Kind+" "+s.Name)
	}
	return strings.Join(out, "; ")
}

func text(src []byte, s lang.Symbol) string { return string(src[s.Start:s.End]) }

func TestPythonSymbols(t *testing.T) {
	a := adapterFor(t, "x.py")
	cases := map[string]string{
		"py-layered/app/repo.py":    "class MemRepo; method MemRepo.__init__; method MemRepo.list; method MemRepo.empty",
		"py-layered/app/service.py": "class BookService; method BookService.__init__; method BookService.list; method BookService.count; function default_service",
		"py-layered/app/api.py":     "function route; function route.register; function list_books",
		"py-layered/app/domain.py":  "class Book; class BookFilter",
	}
	for file, want := range cases {
		if got := symbolList(t, a, readFixture(t, file)); got != want {
			t.Errorf("%s:\n got %s\nwant %s", file, got, want)
		}
	}
}

func TestPythonRanges(t *testing.T) {
	a := adapterFor(t, "x.py")
	src := readFixture(t, "py-layered/app/repo.py")
	s, _ := a.Find(src, "MemRepo.empty")
	if got := text(src, s); got != "@staticmethod\n    def empty() -> \"MemRepo\":\n        return MemRepo()" {
		t.Errorf("decorated method = %q", got)
	}
	if s.Signature != "@staticmethod\n    def empty() -> \"MemRepo\":" {
		t.Errorf("signature = %q", s.Signature)
	}
	svc := readFixture(t, "py-layered/app/service.py")
	l, _ := a.Find(svc, "BookService.list")
	if !strings.HasPrefix(text(svc, l), "# Lists books; a zero limit") {
		t.Errorf("comment not attached: %q", text(svc, l))
	}
	c, _ := a.Find(svc, "BookService")
	if c.Signature != "class BookService:" {
		t.Errorf("class signature = %q", c.Signature)
	}
	api := readFixture(t, "py-layered/app/api.py")
	f, _ := a.Find(api, "list_books")
	if !strings.HasPrefix(text(api, f), `@route("/books")`) {
		t.Errorf("decorator not included: %q", text(api, f))
	}
}

func TestPythonImports(t *testing.T) {
	a := adapterFor(t, "x.py")
	src := readFixture(t, "py-layered/app/service.py")
	imps, _ := a.Imports(src)
	if len(imps) != 2 {
		t.Fatalf("imports = %q", imps)
	}
	at, _ := a.ImportPoint(src)
	if !strings.HasSuffix(string(src[:at]), "from app.repo import MemRepo") {
		t.Errorf("import point after %q", src[:at])
	}
	doc := []byte("\"\"\"Module doc.\"\"\"\n# comment\n\nX = 1\n")
	at, _ = a.ImportPoint(doc)
	if string(doc[:at]) != "\"\"\"Module doc.\"\"\"\n# comment" {
		t.Errorf("preamble import point after %q", doc[:at])
	}
	if a.BareImport() != "import {}" || !a.NestedMembers() {
		t.Error("python import/member settings")
	}
	code := "from app.domain import (\n    Book,\n)\nimport os\n\ndef f():\n    import json\n    return os\n"
	if got := a.Sanitize(code); got != "def f():\n    import json\n    return os" {
		t.Errorf("sanitize = %q", got)
	}
}

func TestTypeScriptSymbols(t *testing.T) {
	a := adapterFor(t, "x.ts")
	cases := map[string]string{
		"ts-layered/src/domain/book.ts":    "interface Book; type BookFilter",
		"ts-layered/src/repo/book.ts":      "interface BookRepo; class MemRepo; method MemRepo.constructor; method MemRepo.list; function newMemRepo",
		"ts-layered/src/service/book.ts":   "class BookService; method BookService.constructor; method BookService.list; method BookService.count",
		"ts-layered/src/transport/http.ts": "type Reply; function listBooks",
	}
	for file, want := range cases {
		if got := symbolList(t, a, readFixture(t, file)); got != want {
			t.Errorf("%s:\n got %s\nwant %s", file, got, want)
		}
	}
}

func TestTypeScriptRanges(t *testing.T) {
	a := adapterFor(t, "x.ts")
	src := readFixture(t, "ts-layered/src/repo/book.ts")
	l, _ := a.Find(src, "MemRepo.list")
	if !strings.HasPrefix(text(src, l), "/** Returns books matching f. */\n  async list(") || l.Signature != "async list(f: BookFilter): Promise<Book[]>" {
		t.Errorf("method = %q sig %q", text(src, l), l.Signature)
	}
	f, _ := a.Find(src, "newMemRepo")
	if !strings.HasPrefix(text(src, f), "export function newMemRepo") {
		t.Errorf("export not included: %q", text(src, f))
	}
	h := readFixture(t, "ts-layered/src/transport/http.ts")
	lb, _ := a.Find(h, "listBooks")
	if !strings.HasPrefix(text(h, lb), "/** GET /books */\nexport const listBooks") || lb.Signature != "export const listBooks = async (svc: BookService, title?: string): Promise<Reply> =>" {
		t.Errorf("arrow = %q sig %q", text(h, lb), lb.Signature)
	}
	at, _ := a.ImportPoint(src)
	if !strings.HasSuffix(string(src[:at]), `from "../domain/book";`) {
		t.Errorf("import point after %q", src[:at])
	}
	code := "import { a } from \"./a\";\nimport type {\n  B,\n} from './b';\nimport \"./side\";\n\nexport function f() {}\n"
	if got := a.Sanitize(code); got != "export function f() {}" {
		t.Errorf("sanitize = %q", got)
	}
}

func TestTSXComponents(t *testing.T) {
	a := adapterFor(t, "x.tsx")
	src := readFixture(t, "tsx-components/src/components/BookList.tsx")
	if got := symbolList(t, a, src); got != "type Book; function useBooks; component BookItem; component BookList" {
		t.Fatalf("symbols = %s", got)
	}
	at, _ := a.ImportPoint(src)
	if string(src[:at]) != `"use client";` {
		t.Errorf("import point after %q (directive must stay first)", src[:at])
	}
	if adapterFor(t, "x.jsx").Lang() != "tsx" {
		t.Error(".jsx should use the tsx adapter")
	}
}

func TestJavaScriptSymbols(t *testing.T) {
	a := adapterFor(t, "x.js")
	src := []byte("import x from \"x\";\n\n/** Doc. */\nexport class Store {\n  get(k) { return k; }\n}\n\nexport const App = () => <div />;\nconst helper = function () {};\nfunction* gen() {}\n")
	if got := symbolList(t, a, src); got != "class Store; method Store.get; component App; function helper; function gen" {
		t.Fatalf("symbols = %s", got)
	}
	for _, ext := range []string{".mjs", ".cjs"} {
		if adapterFor(t, "x"+ext).Lang() != "javascript" {
			t.Errorf("%s adapter", ext)
		}
	}
}

func TestTSAndTSXShareToolchain(t *testing.T) {
	if !reflect.DeepEqual(adapterFor(t, "x.ts").Toolchain(), adapterFor(t, "x.tsx").Toolchain()) {
		t.Fatal("typescript and tsx toolchains drifted apart")
	}
}
