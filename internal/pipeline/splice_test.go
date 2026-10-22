package pipeline

import (
	"os"
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang/builtin"
)

func TestSpliceMembers(t *testing.T) {
	reg, err := builtin.Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		fixture, file, symbol, code, after string
	}{
		// A new inherent method goes into impl Book, not into impl Display for Book.
		{"../../testdata/rust-crate/src/lib.rs", "src/lib.rs", "Book.title_len", "pub fn title_len(&self) -> usize {\n    self.title.len()\n}",
			"        self.title.is_empty()\n    }\n"},
		// After the last member of BookService, the nested class Cache.
		{"../../testdata/csharp-lib/Catalog.cs", "Catalog.cs", "BookService.Clear", "public void Clear() => _books.Clear();",
			"        public void Clear() { }\n    }\n"},
	} {
		src, err := os.ReadFile(c.fixture)
		if err != nil {
			t.Fatal(err)
		}
		a := reg.ForFile(c.file)
		out, err := splice(a, src, Task{File: c.file, Symbol: c.symbol}, c.code)
		if err != nil {
			t.Fatalf("%s: %v", c.symbol, err)
		}
		i := strings.Index(string(out), c.after)
		if i < 0 {
			t.Fatalf("%s: anchor text missing", c.symbol)
		}
		rest := string(out[i+len(c.after):])
		first := strings.TrimSpace(strings.SplitN(c.code, "\n", 2)[0])
		if !strings.HasPrefix(strings.TrimLeft(rest, " \n"), first) {
			t.Errorf("%s inserted in the wrong place:\n%s", c.symbol, out)
		}
		if _, err := a.Find(out, c.symbol); err != nil {
			t.Errorf("%s not found after insert: %v", c.symbol, err)
		}
	}
}
