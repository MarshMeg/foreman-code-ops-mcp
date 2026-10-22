// Package builtin wires the embedded adapters to the compiled-in tree-sitter grammars.
package builtin

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"unsafe"

	tscs "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjs "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tspy "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsrs "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"github.com/MarshMeg/foreman-code-ops-mcp/adapters"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang/generic"
)

// Grammars are the tree-sitter grammars compiled into the binary, by name.
func Grammars() map[string]unsafe.Pointer {
	return map[string]unsafe.Pointer{
		"go":         tsgo.Language(),
		"python":     tspy.Language(),
		"typescript": tsts.LanguageTypescript(),
		"tsx":        tsts.LanguageTSX(),
		"javascript": tsjs.Language(),
		"rust":       tsrs.Language(),
		"c_sharp":    tscs.Language(),
	}
}

// ProjectAdapters is the project directory holding project adapters, one per subdirectory.
const ProjectAdapters = ".foreman/adapters"

// Registry loads every embedded adapter.
func Registry() (*lang.Registry, error) {
	list, err := load(adapters.FS, "built-in")
	if err != nil {
		return nil, err
	}
	return lang.NewRegistry(list...)
}

// RegistryFor loads the embedded adapters plus the project's own adapters from
// <root>/.foreman/adapters/<name>/adapter.yaml. A project adapter replaces the built-in one
// with the same lang, or adds a language; its grammar must be compiled in. It returns the
// languages that came from the project.
func RegistryFor(root string) (*lang.Registry, []string, error) {
	list, err := load(adapters.FS, "built-in")
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(root, filepath.FromSlash(ProjectAdapters))
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			r, err := lang.NewRegistry(list...)
			return r, nil, err
		}
		return nil, nil, err
	}
	own, err := load(os.DirFS(dir), ProjectAdapters)
	if err != nil {
		return nil, nil, err
	}
	var names []string
	for _, a := range own {
		names = append(names, a.Lang())
		list = slices.DeleteFunc(list, func(b lang.Adapter) bool { return b.Lang() == a.Lang() })
	}
	r, err := lang.NewRegistry(append(list, own...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ProjectAdapters, err)
	}
	return r, names, nil
}

func load(fsys fs.FS, origin string) ([]lang.Adapter, error) {
	dirs, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	grammars := Grammars()
	var list []lang.Adapter
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		a, err := generic.Load(fsys, d.Name(), grammars)
		if err != nil {
			return nil, fmt.Errorf("%s adapter %s: %w", origin, d.Name(), err)
		}
		list = append(list, a)
	}
	return list, nil
}
