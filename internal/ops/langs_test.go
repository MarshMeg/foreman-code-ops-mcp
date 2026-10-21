package ops

import (
	"context"
	"strings"
	"testing"

	tu "github.com/MarshMeg/foreman-code-ops/internal/testutil"
)

func TestUpsertPython(t *testing.T) {
	tu.RequireTools(t, "ruff", "pyright")
	llm := tu.NewFakeLLM(t)
	llm.Respond = func(p string) tu.Answer {
		switch {
		case strings.Contains(p, "`BookService.count`"):
			return tu.Answer{Code: "def count(self) -> int:\n    \"\"\"Number of books.\"\"\"\n    return len(self.repo.list(BookFilter(limit=10_000)))"}
		case strings.Contains(p, "`BookService.titles`"):
			// The class has a method named list, so the return annotation avoids `list[...]`.
			return tu.Answer{Code: "def titles(self) -> tuple[str, ...]:\n    return tuple(sorted(b.title for b in self.list(BookFilter())))"}
		case strings.Contains(p, "`get_book`"):
			return tu.Answer{Code: "@route(\"/books/{id}\")\ndef get_book(svc: BookService, id: str) -> Book | None:\n    return next((b for b in svc.list(BookFilter()) if b.id == id), None)",
				Imports: []string{"from app.domain import Book"}}
		}
		t.Errorf("unexpected prompt:\n%s", p)
		return tu.Answer{}
	}
	o, dir := upsertOpsIn(t, "../../testdata/py-layered", llm)
	ctx := context.Background()

	for _, req := range []UpsertRequest{
		{File: "app/service.py", Symbol: "BookService.count", Kind: "method", Spec: "count all books", Interface: "def count(self) -> int"},
		{File: "app/service.py", Symbol: "BookService.titles", Kind: "method", Spec: "sorted titles", Interface: "def titles(self) -> tuple[str, ...]"},
		{File: "app/api.py", Symbol: "get_book", Kind: "function", Spec: "GET /books/{id}", Interface: "def get_book(svc: BookService, id: str) -> Book | None"},
	} {
		res, err := o.Upsert(ctx, req)
		if err != nil || res.Status != StatusApplied {
			t.Fatalf("%s: %+v %v", req.Symbol, res, err)
		}
	}
	svc := tu.ReadFile(t, dir, "app/service.py")
	for _, want := range []string{
		"    def count(self) -> int:\n        \"\"\"Number of books.\"\"\"\n        return len(self.repo.list(BookFilter(limit=10_000)))\n\n    def titles(self) -> tuple[str, ...]:\n        return tuple(sorted(",
		"\n\ndef default_service() -> BookService:",
	} {
		if !strings.Contains(svc, want) {
			t.Errorf("service.py lacks %q:\n%s", want, svc)
		}
	}
	api := tu.ReadFile(t, dir, "app/api.py")
	if !strings.Contains(api, "from app.domain import Book, BookFilter") || !strings.Contains(api, "@route(\"/books/{id}\")\ndef get_book(") {
		t.Errorf("api.py:\n%s", api)
	}
}

func TestUpsertPythonMethodNeedsMembersOrAnchor(t *testing.T) {
	tu.RequireTools(t, "ruff")
	llm := tu.NewFakeLLM(t)
	o, _ := upsertOpsIn(t, "../../testdata/py-layered", llm)
	_, err := o.Upsert(context.Background(), UpsertRequest{File: "app/domain.py", Symbol: "BookFilter.normalized", Kind: "method",
		Spec: "x", Interface: "def normalized(self) -> BookFilter"})
	if err == nil || !strings.Contains(err.Error(), "no members") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpsertTypeScript(t *testing.T) {
	tu.RequireTools(t, "tsc", "prettier")
	llm := tu.NewFakeLLM(t)
	llm.Respond = func(p string) tu.Answer {
		switch {
		case strings.Contains(p, "`BookService.titles`"):
			return tu.Answer{Code: "/** Sorted titles. */\nasync titles(): Promise<string[]> {\nconst books = await this.list({});\nreturn books.map((b) => b.title).sort();\n}"}
		case strings.Contains(p, "`countBooks`"):
			return tu.Answer{Code: "/** GET /books/count */\nexport const countBooks = async (svc: BookService): Promise<Reply> => ({ status: 200, body: await svc.count() });"}
		}
		t.Errorf("unexpected prompt:\n%s", p)
		return tu.Answer{}
	}
	o, dir := upsertOpsIn(t, "../../testdata/ts-layered", llm)
	ctx := context.Background()
	for _, req := range []UpsertRequest{
		{File: "src/service/book.ts", Symbol: "BookService.titles", Kind: "method", Spec: "sorted titles", Interface: "async titles(): Promise<string[]>"},
		{File: "src/transport/http.ts", Symbol: "countBooks", Kind: "function", Spec: "count", Interface: "export const countBooks = async (svc: BookService): Promise<Reply>"},
	} {
		res, err := o.Upsert(ctx, req)
		if err != nil || res.Status != StatusApplied {
			t.Fatalf("%s: %+v %v", req.Symbol, res, err)
		}
	}
	svc := tu.ReadFile(t, dir, "src/service/book.ts")
	if !strings.Contains(svc, "    return books.length;\n  }\n\n  /** Sorted titles. */\n  async titles(): Promise<string[]> {\n    const books = await this.list({});") {
		t.Errorf("service.ts:\n%s", svc)
	}
}

func TestUpsertTSXComponent(t *testing.T) {
	tu.RequireTools(t, "tsc", "prettier")
	llm := tu.NewFakeLLM(t, tu.Answer{Code: "/** Shows how many books there are. */\nexport function BookCount({ books }: { books: Book[] }) {\n  return <p>{books.length} books</p>;\n}"})
	o, dir := upsertOpsIn(t, "../../testdata/tsx-components", llm)
	res, err := o.Upsert(context.Background(), UpsertRequest{File: "src/components/BookList.tsx", Symbol: "BookCount", Kind: "component",
		Spec: "show the number of books", Interface: "export function BookCount({ books }: { books: Book[] })", Anchor: "BookList"})
	if err != nil || res.Status != StatusApplied {
		t.Fatalf("result = %+v %v", res, err)
	}
	if !strings.HasSuffix(tu.ReadFile(t, dir, "src/components/BookList.tsx"), "export function BookCount({ books }: { books: Book[] }) {\n  return <p>{books.length} books</p>;\n}\n") {
		t.Errorf("file:\n%s", tu.ReadFile(t, dir, "src/components/BookList.tsx"))
	}
	if !strings.Contains(llm.Prompts()[0], "## TypeScript / TSX") {
		t.Error("tsx prompt fragment missing")
	}
}
