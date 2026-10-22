package ops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/gitops"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/links"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/workspace"
)

// LinksResult is the outcome of DiscoverLinks.
type LinksResult struct {
	Result
	Map *links.Map
}

// DiscoverLinks scans all services, writes .foreman/links.json and commits it.
func (o *Ops) DiscoverLinks(ctx context.Context) (LinksResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	m, err := o.links.Discover(nil)
	if err != nil {
		return LinksResult{}, err
	}
	cs := workspace.New(o.proj.Root)
	if err := cs.Write(links.RelPath, m.Marshal()); err != nil {
		return LinksResult{}, err
	}
	res, err := o.commitPlain(ctx, cs, gitops.Message{Subject: fmt.Sprintf("links: %d routes, %d calls, %d links", len(m.Routes), len(m.Calls), len(m.Links))})
	return LinksResult{Result: res, Map: m}, err
}

// ErrNoLinks means discover_links has not been run.
var ErrNoLinks = errors.New("no .foreman/links.json yet; run discover_links first")

// GetLinks returns links touching a service, file or symbol.
func (o *Ops) GetLinks(service, file, symbol string) ([]links.Link, error) {
	m, err := links.Load(o.proj.Root)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrNoLinks
	}
	if file != "" {
		rel, _, err := o.proj.Resolve(file)
		if err != nil {
			return nil, err
		}
		file = rel
	}
	return m.Query(service, file, symbol), nil
}

// crossServiceCallers lists calls from other services to the symbol, from links.json.
func (o *Ops) crossServiceCallers(rel, symbol string) []string {
	m, err := links.Load(o.proj.Root)
	if err != nil || m == nil {
		return nil
	}
	var out []string
	for _, l := range m.Callers(rel, symbol) {
		out = append(out, "called from another service: "+l.String())
	}
	return out
}

// refreshLinks recomputes links.json for the candidate changes when the project keeps one,
// so the map is committed together with the code. The caller holds o.mu.
func (o *Ops) refreshLinks(cs *workspace.Changeset) error {
	if _, err := os.Stat(filepath.Join(o.proj.Root, filepath.FromSlash(links.RelPath))); err != nil {
		return nil
	}
	files := cs.Files()
	if len(files) == 1 && files[0] == links.RelPath {
		return nil
	}
	m, err := o.links.Discover(func(rel string) ([]byte, bool, error) { return cs.Read(rel) })
	if err != nil {
		return err
	}
	next := m.Marshal()
	cur, _, err := cs.Read(links.RelPath)
	if err != nil || bytes.Equal(cur, next) {
		return err
	}
	return cs.Write(links.RelPath, next)
}
