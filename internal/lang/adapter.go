// Package lang defines language adapters: how symbols are found in source files
// and where new code goes.
package lang

import (
	"errors"
	"fmt"
	"strings"
)

// Symbol is a top-level or nested definition in a source file.
type Symbol struct {
	// Name is qualified by enclosing definitions or receiver: "BookService.List".
	Name string
	// Kind is the adapter-defined kind: function, method, type, interface, class, component, ...
	Kind string
	// Start and End delimit the symbol in bytes, including attached leading comments.
	Start, End int
	// StartLine and EndLine are 1-based and inclusive.
	StartLine, EndLine int
	// Signature is the declaration without its body.
	Signature string
}

// NewFileOpts parameterizes the content of a new file.
type NewFileOpts struct {
	// Package is the package name for languages that need one; empty lets the adapter infer it.
	Package string
	// Siblings holds the contents of other files in the same directory with the same extension.
	Siblings [][]byte
}

// Adapter handles one language.
type Adapter interface {
	Lang() string
	Extensions() []string
	// Symbols lists all definitions in source order.
	Symbols(src []byte) ([]Symbol, error)
	// Find returns the symbol with the given qualified name.
	Find(src []byte, name string) (Symbol, error)
	// NewFile returns the initial content of a new file at path.
	NewFile(path string, opts NewFileOpts) ([]byte, error)
	// Toolchain describes the gate steps for this language.
	Toolchain() Toolchain
	// PromptFragment is language guidance appended to the generator's system prompt.
	PromptFragment() string
	// Imports returns the import declarations of a file, as source text.
	Imports(src []byte) ([]string, error)
	// Sanitize removes parts a model should not have produced (package clause, imports).
	Sanitize(code string) string
	// ImportPoint is the byte offset where new import lines go: after the last import, or
	// after the file preamble (comments, docstring, directives).
	ImportPoint(src []byte) (int, error)
	// BareImport turns a bare module name into an import statement ("import {}"); "" when
	// the language needs more than a module name.
	BareImport() string
	// NestedMembers is true when methods live inside their class body (Python, TypeScript).
	NestedMembers() bool
}

// ErrNotFound is returned (wrapped in *NotFoundError) when a symbol does not exist.
var ErrNotFound = errors.New("symbol not found")

// NotFoundError carries close candidates to help the caller fix the name.
type NotFoundError struct {
	Name       string
	Candidates []string
}

func (e *NotFoundError) Error() string {
	msg := fmt.Sprintf("symbol %q not found", e.Name)
	if len(e.Candidates) > 0 {
		msg += "; similar: " + strings.Join(e.Candidates, ", ")
	}
	return msg
}

func (e *NotFoundError) Unwrap() error { return ErrNotFound }

// ErrAmbiguous is returned when several definitions share a qualified name.
var ErrAmbiguous = errors.New("ambiguous symbol")
