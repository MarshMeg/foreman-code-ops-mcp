package gate

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/workspace"
)

// group is a set of files and directories checked together in one root.
type group struct {
	adapter lang.Adapter
	root    string   // project-relative
	files   []string // project-relative files (existing after the change)
	dirs    []string // project-relative directories checked recursively
	all     bool
	deleted bool // a package disappeared; only scope=all steps can see the fallout
}

func (gr *group) packages(tc lang.Toolchain) []string {
	if gr.all {
		return []string{tc.AllPackages}
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, d := range gr.dirs {
		add(pattern(tc.RecursivePattern, relTo(gr.root, d)))
	}
	for _, f := range gr.files {
		add(pattern(tc.PackagePattern, relTo(gr.root, path.Dir(f))))
	}
	sort.Strings(out)
	return out
}

func (g *Gate) groups(cs *workspace.Changeset, paths []string) ([]*group, []string, error) {
	byKey := map[string]*group{}
	var order []string
	get := func(a lang.Adapter, root string) *group {
		key := a.Lang() + "\x00" + root
		if gr, ok := byKey[key]; ok {
			return gr
		}
		gr := &group{adapter: a, root: root}
		byKey[key] = gr
		order = append(order, key)
		return gr
	}
	var warnings []string
	addFile := func(rel string, exists bool) {
		a := g.reg.ForFile(rel)
		if a == nil || len(a.Toolchain().Check) == 0 {
			return
		}
		gr := get(a, g.rootFor(a, rel))
		if exists || g.dirHasLangFiles(cs, a, path.Dir(rel)) {
			gr.files = append(gr.files, rel)
		} else {
			gr.deleted = true
		}
	}

	for _, rel := range cs.Files() {
		_, exists, err := cs.Read(rel)
		if err != nil {
			return nil, nil, err
		}
		addFile(rel, exists)
	}
	for _, p := range paths {
		rel, abs, err := g.proj.Resolve(p)
		if err != nil {
			return nil, nil, err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, nil, fmt.Errorf("check path %q: %w", p, err)
		}
		if !info.IsDir() {
			addFile(rel, true)
			continue
		}
		adapters := g.adaptersForDir(rel)
		if len(adapters) == 0 {
			warnings = append(warnings, fmt.Sprintf("no language detected for directory %q", rel))
		}
		for _, a := range adapters {
			gr := get(a, g.rootFor(a, rel+"/x"))
			gr.dirs = append(gr.dirs, rel)
		}
	}
	if cs.Empty() && len(paths) == 0 {
		for _, a := range g.reg.All() {
			if len(a.Toolchain().Check) == 0 {
				continue
			}
			for _, s := range g.proj.Workspace.Services {
				if s.Lang == a.Lang() {
					get(a, cleanRoot(s.Root)).all = true
				}
			}
			if len(g.proj.Workspace.Services) == 0 {
				if root, ok := g.markerRoot(a, "."); ok {
					get(a, root).all = true
				}
			}
		}
	}
	out := make([]*group, 0, len(order))
	for _, k := range order {
		if gr := byKey[k]; gr.all || gr.deleted || len(gr.files) > 0 || len(gr.dirs) > 0 {
			out = append(out, gr)
		}
	}
	return out, warnings, nil
}

// rootFor returns the project-relative directory commands for rel run in: the owning
// service when it has one of the adapter's root markers (or the adapter has none), else the
// nearest ancestor with a root marker, else the project root.
func (g *Gate) rootFor(a lang.Adapter, rel string) string {
	if s := g.proj.ServiceFor(rel); s != nil {
		root := cleanRoot(s.Root)
		markers := a.Toolchain().RootMarkers
		if len(markers) == 0 || g.hasMarker(root, markers) {
			return root
		}
	}
	if root, ok := g.markerRoot(a, path.Dir(rel)); ok {
		return root
	}
	return "."
}

// markerRoot walks up from dir looking for one of the adapter's root markers.
func (g *Gate) markerRoot(a lang.Adapter, dir string) (string, bool) {
	markers := a.Toolchain().RootMarkers
	for d := dir; ; d = path.Dir(d) {
		if g.hasMarker(d, markers) {
			return d, true
		}
		if d == "." || d == "/" {
			return "", false
		}
	}
}

// adaptersForDir picks adapters for a directory: the owning service's language, or every
// adapter with a root marker at or above it.
func (g *Gate) adaptersForDir(rel string) []lang.Adapter {
	if s := g.proj.ServiceFor(rel + "/x"); s != nil {
		if a := g.reg.ForLang(s.Lang); a != nil {
			return []lang.Adapter{a}
		}
	}
	var out []lang.Adapter
	for _, a := range g.reg.All() {
		if _, ok := g.markerRoot(a, rel); ok {
			out = append(out, a)
		}
	}
	return out
}

// dirHasLangFiles reports whether dir still holds files of the adapter's language after cs.
func (g *Gate) dirHasLangFiles(cs *workspace.Changeset, a lang.Adapter, dir string) bool {
	entries, _ := os.ReadDir(filepath.Join(g.proj.Root, filepath.FromSlash(dir)))
	for _, e := range entries {
		rel := path.Join(dir, e.Name())
		if e.Type().IsRegular() && g.reg.ForFile(rel) == a {
			if _, exists, _ := cs.Read(rel); exists {
				return true
			}
		}
	}
	for _, rel := range cs.Files() {
		if path.Dir(rel) == dir && g.reg.ForFile(rel) == a {
			if _, exists, _ := cs.Read(rel); exists {
				return true
			}
		}
	}
	return false
}

// hasMarker reports whether dir holds one of markers; a marker may be a glob (*.csproj).
func (g *Gate) hasMarker(dir string, markers []string) bool {
	return lang.HasMarker(filepath.Join(g.proj.Root, filepath.FromSlash(dir)), markers)
}

func severity(s lang.Step) string {
	if s.Severity == "" {
		return SeverityError
	}
	return s.Severity
}

func stepName(s lang.Step) string {
	switch {
	case s.Name != "":
		return s.Name
	case s.Builtin != "":
		return s.Builtin
	default:
		return strings.Join(s.Run, " ")
	}
}

// expand substitutes placeholders. An argument equal to a placeholder is replaced by all its
// values (possibly none); placeholders inside a longer argument take the first value.
func expand(argv []string, vars map[string][]string) []string {
	var out []string
	for _, arg := range argv {
		if vals, ok := vars[arg]; ok {
			out = append(out, vals...)
			continue
		}
		for k, vals := range vars {
			if strings.Contains(arg, k) && len(vals) > 0 {
				arg = strings.ReplaceAll(arg, k, vals[0])
			}
		}
		out = append(out, arg)
	}
	return out
}

func pattern(p, dir string) string {
	if p == "" {
		p = "{dir}"
	}
	return strings.ReplaceAll(p, "{dir}", dir)
}

// relTo makes a project-relative path relative to a project-relative root.
func relTo(root, rel string) string {
	root = cleanRoot(root)
	if root == "." {
		return rel
	}
	if rel == root {
		return "."
	}
	return strings.TrimPrefix(rel, root+"/")
}

func relAll(root string, rels []string) []string {
	out := make([]string, len(rels))
	for i, r := range rels {
		out[i] = relTo(root, r)
	}
	return out
}

func cleanRoot(r string) string {
	if r == "" {
		return "."
	}
	return path.Clean(r)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// MissingTools reports required (non-optional, ungrouped) commands that cannot be found, for
// the roots of languages the project uses.
func (g *Gate) MissingTools() []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range g.reg.All() {
		var roots []string
		for _, s := range g.proj.Workspace.Services {
			if root := cleanRoot(s.Root); g.hasMarker(root, a.Toolchain().RootMarkers) {
				roots = append(roots, root)
			}
		}
		if len(roots) == 0 && g.hasMarker(".", a.Toolchain().RootMarkers) {
			roots = []string{"."}
		}
		for _, root := range roots {
			e := g.plainEnv(a, root)
			tc := a.Toolchain()
			for _, s := range append(append([]lang.Step{}, tc.Fix...), tc.Check...) {
				if len(s.Run) == 0 || s.Optional || s.Group != "" || !e.applies(s.When) {
					continue
				}
				key := root + "\x00" + s.Run[0]
				if _, ok := e.resolve(s.Run[0]); !ok && !seen[key] {
					seen[key] = true
					out = append(out, fmt.Sprintf("%s (%s): %q not found; the %s step will fail", a.Lang(), root, s.Run[0], stepName(s)))
				}
			}
		}
	}
	return out
}
