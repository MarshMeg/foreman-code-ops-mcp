// Package project models the project root and its services (.foreman/workspace.yaml)
// and resolves tool-supplied paths safely inside the root.
package project

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
)

// Service is one root in a monorepo.
type Service struct {
	Name     string         `yaml:"name"`
	Root     string         `yaml:"root"`
	Lang     string         `yaml:"lang"`
	Exposes  map[string]any `yaml:"exposes,omitempty"`
	Consumes []Consume      `yaml:"consumes,omitempty"`
}

// Consume declares that a service calls another one.
type Consume struct {
	Service    string `yaml:"service"`
	BaseURLEnv string `yaml:"base_url_env,omitempty"`
}

// Workspace is the content of .foreman/workspace.yaml.
type Workspace struct {
	Services []Service `yaml:"services"`
}

// Project is an opened project root.
type Project struct {
	Root      string // absolute, symlinks resolved
	Workspace Workspace
}

// WorkspacePath returns <root>/.foreman/workspace.yaml.
func WorkspacePath(root string) string {
	return filepath.Join(root, config.DirName, "workspace.yaml")
}

// Open loads the project at root. A missing workspace.yaml means no declared services.
func Open(root string) (*Project, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if abs, err = filepath.EvalSymlinks(abs); err != nil {
		return nil, err
	}
	p := &Project{Root: abs}
	data, err := os.ReadFile(WorkspacePath(abs))
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p.Workspace); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", WorkspacePath(abs), err)
	}
	if err := p.Workspace.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspacePath(abs), err)
	}
	return p, nil
}

func (w Workspace) validate() error {
	names := map[string]bool{}
	var errs []error
	for i, s := range w.Services {
		if s.Name == "" {
			errs = append(errs, fmt.Errorf("services[%d]: name is required", i))
		}
		if names[s.Name] {
			errs = append(errs, fmt.Errorf("services[%d]: duplicate name %q", i, s.Name))
		}
		names[s.Name] = true
		if _, err := cleanRel(s.Root); err != nil {
			errs = append(errs, fmt.Errorf("services[%d] %s: root: %w", i, s.Name, err))
		}
	}
	return errors.Join(errs...)
}

// ServiceFor returns the service whose root most specifically contains rel, or nil.
func (p *Project) ServiceFor(rel string) *Service {
	var best *Service
	bestLen := -1
	for i := range p.Workspace.Services {
		s := &p.Workspace.Services[i]
		root, _ := cleanRel(s.Root)
		if within(rel, root) && len(root) > bestLen {
			best, bestLen = s, len(root)
		}
	}
	return best
}

// ServiceNames returns the sorted, de-duplicated names of the services owning paths.
func (p *Project) ServiceNames(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, rel := range paths {
		if s := p.ServiceFor(rel); s != nil && !seen[s.Name] {
			seen[s.Name] = true
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

func within(rel, root string) bool {
	return root == "." || rel == root || strings.HasPrefix(rel, root+"/")
}

// Resolve validates a tool-supplied path and returns it cleaned (slash-separated, relative
// to the root) together with its absolute form. It rejects absolute paths, paths escaping
// the root (also through symlinks) and anything inside .git.
func (p *Project) Resolve(path string) (rel, abs string, err error) {
	rel, err = cleanRel(path)
	if err != nil {
		return "", "", err
	}
	if rel == "." {
		return "", "", fmt.Errorf("path %q is the project root", path)
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".git" {
			return "", "", fmt.Errorf("path %q is inside .git", path)
		}
	}
	abs = filepath.Join(p.Root, filepath.FromSlash(rel))
	// The deepest existing ancestor must resolve inside the root.
	probe := abs
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if real != p.Root && !strings.HasPrefix(real, p.Root+string(filepath.Separator)) {
				return "", "", fmt.Errorf("path %q resolves outside the project", path)
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", "", err
		}
		probe = filepath.Dir(probe)
	}
	return rel, abs, nil
}

func cleanRel(path string) (string, error) {
	if path == "" {
		return "", errors.New("empty path")
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("path %q must be relative to the project root", path)
	}
	c := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("path %q escapes the project root", path)
	}
	return c, nil
}
