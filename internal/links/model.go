// Package links discovers who calls whom between the services of a monorepo: HTTP routes
// and client calls found in source, contracts from OpenAPI and protobuf files, and base URLs
// from docker-compose and .env files. The result is stored in .foreman/links.json.
package links

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
)

// Confidence levels of a link.
const (
	// Contract: both sides match an operation in the server's OpenAPI or protobuf contract.
	Contract = "contract"
	// Matched: HTTP method and path match.
	Matched = "matched"
	// Guessed: only the path matches, after prefix stripping, or the path was built dynamically.
	Guessed = "guessed"
)

// AnyMethod matches every HTTP method (Handle without a method, Django paths).
const AnyMethod = "ANY"

// Endpoint is one side of a link.
type Endpoint struct {
	Service string `json:"service"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Symbol  string `json:"symbol,omitempty"`
	Method  string `json:"method"`
	Path    string `json:"path"`
}

// Route is a server endpoint.
type Route struct {
	Endpoint
	Framework string `json:"framework"`
	// Contract is true when an OpenAPI/proto operation declares this route.
	Contract bool `json:"contract,omitempty"`
}

// Call is a client request found in code.
type Call struct {
	Endpoint
	Client string `json:"client"`
	// Target is the service the base URL points to, when known.
	Target string `json:"target,omitempty"`
	// Dynamic is true when the path had to be partly guessed (variables in the URL).
	Dynamic bool `json:"dynamic,omitempty"`
	// Raw is the URL expression as written.
	Raw string `json:"raw,omitempty"`
}

// Link connects a call to the route it reaches.
type Link struct {
	From       Endpoint `json:"from"`
	To         Endpoint `json:"to"`
	Confidence string   `json:"confidence"`
}

func (l Link) String() string {
	return fmt.Sprintf("%s → %s %s → %s [%s]", endpointName(l.From), l.To.Method, l.To.Path, endpointName(l.To), l.Confidence)
}

func endpointName(e Endpoint) string {
	if e.Symbol != "" {
		return e.Service + "." + e.Symbol
	}
	return fmt.Sprintf("%s:%s:%d", e.Service, e.File, e.Line)
}

// Map is the content of .foreman/links.json.
type Map struct {
	Version int     `json:"version"`
	Routes  []Route `json:"routes"`
	Calls   []Call  `json:"calls"`
	Links   []Link  `json:"links"`
}

// Path returns <root>/.foreman/links.json.
func Path(root string) string { return filepath.Join(root, config.DirName, "links.json") }

// RelPath is the project-relative path of links.json.
var RelPath = config.DirName + "/links.json"

// Load reads links.json; a missing file returns (nil, nil).
func Load(root string) (*Map, error) {
	b, err := os.ReadFile(Path(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Map
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(root), err)
	}
	return &m, nil
}

// Marshal renders the map deterministically (sorted, indented) so diffs stay small.
func (m *Map) Marshal() []byte {
	m.sort()
	b, _ := json.MarshalIndent(m, "", "  ")
	return append(b, '\n')
}

func (m *Map) sort() {
	key := func(e Endpoint) string {
		return fmt.Sprintf("%s\x00%s\x00%08d\x00%s\x00%s", e.Service, e.File, e.Line, e.Method, e.Path)
	}
	sort.Slice(m.Routes, func(i, j int) bool { return key(m.Routes[i].Endpoint) < key(m.Routes[j].Endpoint) })
	sort.Slice(m.Calls, func(i, j int) bool { return key(m.Calls[i].Endpoint) < key(m.Calls[j].Endpoint) })
	sort.Slice(m.Links, func(i, j int) bool {
		a, b := key(m.Links[i].From)+key(m.Links[i].To), key(m.Links[j].From)+key(m.Links[j].To)
		return a < b
	})
	if m.Routes == nil {
		m.Routes = []Route{}
	}
	if m.Calls == nil {
		m.Calls = []Call{}
	}
	if m.Links == nil {
		m.Links = []Link{}
	}
}

// Query returns links touching service, file or symbol (empty filters match everything).
func (m *Map) Query(service, file, symbol string) []Link {
	var out []Link
	match := func(e Endpoint) bool {
		return (service == "" || e.Service == service) && (file == "" || e.File == file) &&
			(symbol == "" || e.Symbol == symbol || strings.HasSuffix(e.Symbol, "."+symbol))
	}
	for _, l := range m.Links {
		if match(l.From) || match(l.To) {
			out = append(out, l)
		}
	}
	return out
}

// Callers returns links whose target is the symbol in file.
func (m *Map) Callers(file, symbol string) []Link {
	var out []Link
	for _, l := range m.Links {
		if l.To.File == file && l.To.Symbol == symbol {
			out = append(out, l)
		}
	}
	return out
}

var (
	placeholder = regexp.MustCompile(`\$\{[^}]*\}|\{[^}/]*\}|<[^>/]*>|%[sdvq]|:[A-Za-z_]\w*\??|\*\w*`)
	multiSlash  = regexp.MustCompile(`/{2,}`)
)

// NormalizePath reduces a route or URL path to a comparable form: scheme and host removed,
// query dropped, every parameter ({id}, :id, <int:id>, ${id}, %d) replaced by "{}",
// no trailing slash.
func NormalizePath(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.Index(p, "://"); i >= 0 {
		rest := p[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			p = rest[j:]
		} else {
			p = "/"
		}
	}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = placeholder.ReplaceAllString(p, "{}")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = multiSlash.ReplaceAllString(p, "/")
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// JoinPath joins a router prefix and a route path.
func JoinPath(prefix, p string) string {
	if prefix == "" {
		return p
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(p, "/")
}
