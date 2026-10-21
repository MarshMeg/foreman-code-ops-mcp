package lang

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Registry maps file extensions to adapters.
type Registry struct {
	byExt  map[string]Adapter
	byLang map[string]Adapter
}

// NewRegistry builds a registry; a later adapter claiming an extension is an error.
func NewRegistry(adapters ...Adapter) (*Registry, error) {
	r := &Registry{byExt: map[string]Adapter{}, byLang: map[string]Adapter{}}
	for _, a := range adapters {
		if _, dup := r.byLang[a.Lang()]; dup {
			return nil, fmt.Errorf("duplicate adapter for language %q", a.Lang())
		}
		r.byLang[a.Lang()] = a
		for _, ext := range a.Extensions() {
			ext = strings.ToLower(ext)
			if other, dup := r.byExt[ext]; dup {
				return nil, fmt.Errorf("extension %s claimed by both %s and %s", ext, other.Lang(), a.Lang())
			}
			r.byExt[ext] = a
		}
	}
	return r, nil
}

// ForFile returns the adapter for path, or nil if no adapter handles its extension.
func (r *Registry) ForFile(path string) Adapter {
	return r.byExt[strings.ToLower(filepath.Ext(path))]
}

// ForLang returns the adapter for a language name, or nil.
func (r *Registry) ForLang(name string) Adapter { return r.byLang[name] }
