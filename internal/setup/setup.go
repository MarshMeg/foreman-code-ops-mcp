// Package setup prepares a project for foreman (`foreman-code-ops init`): the services draft,
// Claude Code permissions that route source edits through foreman, an optional project MCP
// entry, and a .gitignore line for runtime files. Existing files are merged, never replaced.
package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/MarshMeg/foreman-code-ops/internal/project"
)

// Options configures Init.
type Options struct {
	Root string
	// Extensions handled by foreman; Claude's Edit/Write is denied for them.
	Extensions []string
	// Permissions writes the deny rules into .claude/settings.json.
	Permissions bool
	// MCP registers the server in the project's .mcp.json (not needed with the plugin).
	MCP bool
	// Binary is the command written to .mcp.json.
	Binary string
	// ForceWorkspace overwrites an existing workspace.yaml.
	ForceWorkspace bool
}

// Report lists what Init did, as human-readable lines.
type Report struct {
	Lines []string
}

func (r *Report) add(format string, args ...any) {
	r.Lines = append(r.Lines, fmt.Sprintf(format, args...))
}

// Init prepares the project.
func Init(o Options) (Report, error) {
	var rep Report
	if err := workspace(o, &rep); err != nil {
		return rep, err
	}
	if o.Permissions {
		if err := permissions(o, &rep); err != nil {
			return rep, err
		}
	}
	if o.MCP {
		if err := mcpEntry(o, &rep); err != nil {
			return rep, err
		}
	}
	if err := gitignore(o, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

func workspace(o Options, rep *Report) error {
	path := project.WorkspacePath(o.Root)
	if _, err := os.Stat(path); err == nil && !o.ForceWorkspace {
		rep.add("kept %s (use --force to regenerate)", rel(o.Root, path))
		return nil
	}
	svcs, err := project.Discover(o.Root)
	if err != nil {
		return err
	}
	data, err := project.MarshalWorkspace(project.Workspace{Services: svcs})
	if err != nil {
		return err
	}
	if err := writeFile(path, data); err != nil {
		return err
	}
	rep.add("wrote %s with %d service(s); review it", rel(o.Root, path), len(svcs))
	return nil
}

// DenyRules returns the Edit deny rules for the extensions. Claude Code checks Write,
// MultiEdit and NotebookEdit against Edit rules, so Edit rules cover them all.
func DenyRules(exts []string) []string {
	var out []string
	for _, e := range exts {
		out = append(out, fmt.Sprintf("Edit(**/*%s)", e))
	}
	sort.Strings(out)
	return out
}

func permissions(o Options, rep *Report) error {
	path := filepath.Join(o.Root, ".claude", "settings.json")
	settings, err := readJSON(path)
	if err != nil {
		return err
	}
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	var deny []any
	if d, ok := perms["deny"].([]any); ok {
		deny = d
	}
	var added []string
	for _, rule := range DenyRules(o.Extensions) {
		if !slices.Contains(deny, any(rule)) {
			deny = append(deny, rule)
			added = append(added, rule)
		}
	}
	if len(added) == 0 {
		rep.add("kept %s (deny rules already present)", rel(o.Root, path))
		return nil
	}
	perms["deny"] = deny
	settings["permissions"] = perms
	if err := writeJSON(path, settings); err != nil {
		return err
	}
	rep.add("added to %s: permissions.deny %s (source edits go through foreman)", rel(o.Root, path), strings.Join(added, ", "))
	return nil
}

func mcpEntry(o Options, rep *Report) error {
	path := filepath.Join(o.Root, ".mcp.json")
	doc, err := readJSON(path)
	if err != nil {
		return err
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if _, ok := servers["foreman"]; ok {
		rep.add("kept %s (server foreman already registered)", rel(o.Root, path))
		return nil
	}
	bin := o.Binary
	if bin == "" {
		bin = "foreman-code-ops"
	}
	servers["foreman"] = map[string]any{
		"command": bin,
		"args":    []any{"--project", "${CLAUDE_PROJECT_DIR:-.}"},
	}
	doc["mcpServers"] = servers
	if err := writeJSON(path, doc); err != nil {
		return err
	}
	rep.add("registered MCP server foreman in %s", rel(o.Root, path))
	return nil
}

func gitignore(o Options, rep *Report) error {
	const line = ".foreman/cache/"
	path := filepath.Join(o.Root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) == line || strings.TrimSpace(l) == "/"+line {
			return nil
		}
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	data = append(data, []byte("# foreman-code-ops runtime files\n"+line+"\n")...)
	if err := writeFile(path, data); err != nil {
		return err
	}
	rep.add("added %s to .gitignore", line)
	return nil
}

func readJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w (fix or remove it and rerun init)", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func writeJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return writeFile(path, buf.Bytes())
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return r
	}
	return path
}
