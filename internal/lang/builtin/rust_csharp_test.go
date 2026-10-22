package builtin

import (
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang"
)

func TestRustSymbols(t *testing.T) {
	a := adapterFor(t, "src/lib.rs")
	src := readFixture(t, "rust-crate/src/lib.rs")
	want := "type Book; type Genre; type Catalog; const MAX_BOOKS; interface Repo; method Repo.get; method Repo.count; " +
		"method Book.new; method Book.is_empty; method Book.Display.fmt; type Wrapper; method Wrapper.inner; macro book; function find; " +
		"module tests; function tests.new_sets_title"
	if got := symbolList(t, a, src); got != want {
		t.Errorf("symbols:\n got %s\nwant %s", got, want)
	}
}

func TestRustRanges(t *testing.T) {
	a := adapterFor(t, "src/lib.rs")
	src := readFixture(t, "rust-crate/src/lib.rs")
	for name, want := range map[string]struct{ start, sig string }{
		"Book":                 {"/// A book in the catalog.\n#[derive(Debug, Clone)]\npub struct Book {", "/// A book"},
		"Book.new":             {"/// Creates a book.\n    pub fn new(", "pub fn new(id: u64, title: &str) -> Self"},
		"Repo":                 {"/// Storage for books.\npub trait Repo", "pub trait Repo"},
		"Repo.count":           {"/// Number of books.\n    fn count", "fn count(&self) -> usize"},
		"find":                 {"// Finds a book by title.\npub async fn find", "pub async fn find(books: &[Book], title: &str) -> Option<Book>"},
		"tests.new_sets_title": {"#[test]\n    fn new_sets_title()", "fn new_sets_title()"},
	} {
		s, err := a.Find(src, name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !strings.HasPrefix(text(src, s), want.start) {
			t.Errorf("%s range starts %q, want prefix %q", name, firstLines(text(src, s)), want.start)
		}
		if name != "Book" && s.Signature != want.sig {
			t.Errorf("%s signature = %q, want %q", name, s.Signature, want.sig)
		}
	}
}

func TestRustImportsAndSanitize(t *testing.T) {
	a := adapterFor(t, "src/lib.rs")
	src := readFixture(t, "rust-crate/src/lib.rs")
	imps, err := a.Imports(src)
	if err != nil || strings.Join(imps, "\n") != "use std::collections::HashMap;\nuse std::fmt::{self, Display};\nuse super::*;" {
		t.Fatalf("imports = %q, %v", imps, err)
	}
	code := "use std::fmt;\npub use crate::x::{\n    A,\n    B,\n};\n/// Doc.\npub fn f() {\n    use std::io::Read;\n}\n"
	if got := a.Sanitize(code); got != "/// Doc.\npub fn f() {\n    use std::io::Read;\n}" {
		t.Errorf("sanitize = %q", got)
	}
	// A file without imports gets them after the crate doc comment.
	at, err := a.ImportPoint([]byte("//! Crate.\n#![allow(dead_code)]\n\nfn main() {}\n"))
	if err != nil || at != len("//! Crate.\n#![allow(dead_code)]") {
		t.Errorf("import point = %d, %v", at, err)
	}
}

func TestCSharpSymbols(t *testing.T) {
	a := adapterFor(t, "Catalog.cs")
	src := readFixture(t, "csharp-lib/Catalog.cs")
	want := "class Book; type Genre; interface IBookRepo; method IBookRepo.Get; property IBookRepo.Count; " +
		"class BookService; method BookService.BookService; property BookService.Count; method BookService.Get; " +
		"method BookService.ListAsync; class BookService.Cache; method BookService.Cache.Clear; type Point; type BookAdded"
	if got := symbolList(t, a, src); got != want {
		t.Errorf("symbols:\n got %s\nwant %s", got, want)
	}
}

func TestCSharpRangesAndFile(t *testing.T) {
	a := adapterFor(t, "Catalog.cs")
	src := readFixture(t, "csharp-lib/Catalog.cs")
	for name, want := range map[string]struct{ start, sig string }{
		"BookService":     {"[Serializable]\npublic class BookService", "[Serializable]\npublic class BookService : IBookRepo"},
		"BookService.Get": {"/// <summary>Finds a book.</summary>\n    [Obsolete(\"use Find\")]\n    public Book? Get", "[Obsolete(\"use Find\")]\n    public Book? Get(int id)"},
		// The arrow of an expression-bodied member is its body.
		"BookService.ListAsync": {"public async Task<List<Book>> ListAsync()", "public async Task<List<Book>> ListAsync()"},
	} {
		s, err := a.Find(src, name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !strings.HasPrefix(text(src, s), want.start) {
			t.Errorf("%s range starts %q, want prefix %q", name, firstLines(text(src, s)), want.start)
		}
		if s.Signature != want.sig {
			t.Errorf("%s signature = %q, want %q", name, s.Signature, want.sig)
		}
	}
	imps, _ := a.Imports(src)
	if strings.Join(imps, "\n") != "using System;\nusing System.Collections.Generic;" {
		t.Errorf("imports = %q", imps)
	}
	if got := a.Sanitize("using System.Linq;\nusing static System.Math;\nnamespace Books;\n\npublic int F() => 1;"); got != "public int F() => 1;" {
		t.Errorf("sanitize = %q", got)
	}
	nf, err := a.NewFile("csharp-lib/New.cs", lang.NewFileOpts{Siblings: [][]byte{src}})
	if err != nil || string(nf) != "namespace Books.Catalog;\n" {
		t.Errorf("new file = %q, %v", nf, err)
	}
}

func firstLines(s string) string {
	if i := strings.Index(s, "{"); i > 0 {
		return s[:i]
	}
	return s
}
