package builtin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops/internal/lang"
)

func goAdapter(t *testing.T) lang.Adapter {
	t.Helper()
	r, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	a := r.ForFile("x.go")
	if a == nil || a.Lang() != "go" {
		t.Fatal("no go adapter")
	}
	return a
}

func fixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../../testdata/go-layered", rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGoSymbols(t *testing.T) {
	a := goAdapter(t)
	cases := map[string][]string{
		"internal/domain/book.go":  {"type Book", "type ID", "type Status", "type BookFilter"},
		"internal/repo/book.go":    {"interface BookRepo", "type Page", "method Page.Len", "type memRepo", "function NewMemRepo", "method memRepo.List"},
		"internal/service/book.go": {"type BookService", "function NewBookService", "method BookService.List", "method BookService.Count", "function unusedHelper"},
	}
	for file, want := range cases {
		syms, err := a.Symbols(fixture(t, file))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, s := range syms {
			got = append(got, s.Kind+" "+s.Name)
		}
		if strings.Join(got, "; ") != strings.Join(want, "; ") {
			t.Errorf("%s:\n got %v\nwant %v", file, got, want)
		}
	}
}

func TestGoFindRangesAndSignatures(t *testing.T) {
	a := goAdapter(t)
	src := fixture(t, "internal/service/book.go")

	s, err := a.Find(src, "BookService.List")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src[s.Start:s.End])
	if !strings.HasPrefix(text, "// List returns books matching f.\n// A zero limit") {
		t.Errorf("doc comment not attached: %q", text[:40])
	}
	if !strings.HasSuffix(text, "return s.repo.List(ctx, f)\n}") {
		t.Errorf("unexpected end: %q", text[len(text)-30:])
	}
	if s.Signature != "func (s *BookService) List(ctx context.Context, f domain.BookFilter) ([]domain.Book, error)" {
		t.Errorf("signature = %q", s.Signature)
	}
	if s.StartLine != 22 || s.EndLine != 29 {
		t.Errorf("lines = %d-%d", s.StartLine, s.EndLine)
	}

	// A single-spec type declaration includes the `type` keyword.
	bs, _ := a.Find(src, "BookService")
	if got := string(src[bs.Start:bs.End]); !strings.HasPrefix(got, "// BookService implements") || !strings.Contains(got, "type BookService struct") {
		t.Errorf("BookService range = %q", got)
	}

	// The trailing comment of `var defaultLimit` must not be attached to the next symbol.
	if !strings.HasPrefix(string(src[bs.Start:]), "// BookService") {
		t.Errorf("trailing comment attached")
	}

	// Undocumented function: range starts at func.
	u, _ := a.Find(src, "unusedHelper")
	if string(src[u.Start:u.End]) != "func unusedHelper() {}" {
		t.Errorf("unusedHelper = %q", src[u.Start:u.End])
	}
}

func TestGoGroupedTypeSpec(t *testing.T) {
	a := goAdapter(t)
	src := fixture(t, "internal/domain/book.go")
	s, err := a.Find(src, "Status")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(src[s.Start:s.End]); got != "// Status is the lending state.\n\tStatus int" {
		t.Errorf("grouped spec range = %q", got)
	}
}

func TestGoGenericReceiver(t *testing.T) {
	a := goAdapter(t)
	s, err := a.Find(fixture(t, "internal/repo/book.go"), "Page.Len")
	if err != nil {
		t.Fatal(err)
	}
	if s.Signature != "func (p Page[T]) Len() int" {
		t.Errorf("signature = %q", s.Signature)
	}
}

func TestGoNotFoundAndAmbiguous(t *testing.T) {
	a := goAdapter(t)
	_, err := a.Find(fixture(t, "internal/service/book.go"), "Service.List")
	var nf *lang.NotFoundError
	if !errors.As(err, &nf) || !errors.Is(err, lang.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(nf.Candidates, ",") != "BookService.List" {
		t.Errorf("candidates = %v", nf.Candidates)
	}

	dup := []byte("package p\n\nfunc init() {}\n\nfunc init() {}\n")
	if _, err := a.Find(dup, "init"); !errors.Is(err, lang.ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
}

func TestGoNewFile(t *testing.T) {
	a := goAdapter(t)
	b, _ := a.NewFile("internal/service/extra.go", lang.NewFileOpts{Siblings: [][]byte{fixture(t, "internal/service/book.go")}})
	if string(b) != "package service\n" {
		t.Errorf("from sibling: %q", b)
	}
	b, _ = a.NewFile("internal/my-pkg/x.go", lang.NewFileOpts{})
	if string(b) != "package mypkg\n" {
		t.Errorf("from dir: %q", b)
	}
	b, _ = a.NewFile("x.go", lang.NewFileOpts{Package: "custom"})
	if string(b) != "package custom\n" {
		t.Errorf("explicit: %q", b)
	}
}
