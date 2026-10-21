// Package builtin wires the embedded adapters to the compiled-in tree-sitter grammars.
package builtin

import (
	"fmt"
	"io/fs"
	"unsafe"

	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"

	"github.com/MarshMeg/foreman-code-ops/adapters"
	"github.com/MarshMeg/foreman-code-ops/internal/lang"
	"github.com/MarshMeg/foreman-code-ops/internal/lang/generic"
)

// Grammars are the tree-sitter grammars compiled into the binary, by name.
func Grammars() map[string]unsafe.Pointer {
	return map[string]unsafe.Pointer{
		"go": tsgo.Language(),
	}
}

// Registry loads every embedded adapter.
func Registry() (*lang.Registry, error) {
	dirs, err := fs.ReadDir(adapters.FS, ".")
	if err != nil {
		return nil, err
	}
	grammars := Grammars()
	var list []lang.Adapter
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		a, err := generic.Load(adapters.FS, d.Name(), grammars)
		if err != nil {
			return nil, fmt.Errorf("built-in adapter %s: %w", d.Name(), err)
		}
		list = append(list, a)
	}
	return lang.NewRegistry(list...)
}
