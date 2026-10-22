package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAdapter(t *testing.T, root, name string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, ".foreman", "adapters", name)
	os.MkdirAll(dir, 0o755)
	for f, c := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRegistryForProjectAdapters(t *testing.T) {
	root := t.TempDir()
	if r, own, err := RegistryFor(root); err != nil || own != nil || r.ForLang("rust") == nil {
		t.Fatalf("no project adapters: %v %v", own, err)
	}

	// Replace the built-in Go adapter with one that only knows functions, and add a language
	// on a compiled-in grammar.
	writeAdapter(t, root, "go", map[string]string{
		"adapter.yaml": "lang: go\ngrammar: go\nextensions: [.go]\nsymbols_query: s.scm\n",
		"s.scm":        "(function_declaration name: (identifier) @name) @definition.function\n",
	})
	writeAdapter(t, root, "starlark", map[string]string{
		"adapter.yaml": "lang: starlark\ngrammar: python\nextensions: [.star, .bzl]\nsymbols_query: s.scm\n",
		"s.scm":        "(function_definition name: (identifier) @name) @definition.rule\n",
	})
	r, own, err := RegistryFor(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(own, ",") != "go,starlark" {
		t.Fatalf("own = %v", own)
	}
	src := []byte("package p\n\ntype T struct{}\n\nfunc F() {}\n")
	if got := symbolList(t, r.ForFile("a.go"), src); got != "function F" {
		t.Errorf("project go adapter: %s", got)
	}
	if got := symbolList(t, r.ForFile("BUILD.bzl"), []byte("def my_rule(name):\n    pass\n")); got != "rule my_rule" {
		t.Errorf("starlark: %s", got)
	}
	if r.ForLang("python") == nil {
		t.Error("python adapter dropped")
	}

	writeAdapter(t, root, "bad", map[string]string{
		"adapter.yaml": "lang: bad\ngrammar: cobol\nextensions: [.cob]\nsymbols_query: s.scm\n",
		"s.scm":        "",
	})
	if _, _, err := RegistryFor(root); err == nil || !strings.Contains(err.Error(), `grammar "cobol" is not compiled in (available: c_sharp, go, javascript, python, rust, tsx, typescript)`) {
		t.Fatalf("bad grammar: %v", err)
	}
}
