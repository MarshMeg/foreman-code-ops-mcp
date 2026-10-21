package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(root, rel))
	return string(b)
}

func TestInitFresh(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	rep, err := Init(Options{Root: root, Extensions: []string{".go"}, Permissions: true, MCP: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Lines) != 4 {
		t.Fatalf("report = %v", rep.Lines)
	}
	if !strings.Contains(read(t, root, ".foreman/workspace.yaml"), "lang: go") {
		t.Error("workspace.yaml")
	}
	var settings map[string]any
	json.Unmarshal([]byte(read(t, root, ".claude/settings.json")), &settings)
	deny := settings["permissions"].(map[string]any)["deny"].([]any)
	if len(deny) != 1 || deny[0] != "Edit(**/*.go)" {
		t.Errorf("deny = %v", deny)
	}
	mcp := read(t, root, ".mcp.json")
	if !strings.Contains(mcp, `"command": "foreman-code-ops"`) || !strings.Contains(mcp, "${CLAUDE_PROJECT_DIR:-.}") {
		t.Errorf(".mcp.json = %s", mcp)
	}
	if !strings.Contains(read(t, root, ".gitignore"), ".foreman/cache/") {
		t.Error(".gitignore")
	}
}

func TestInitMergesExisting(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/settings.json", `{"model": "opus", "permissions": {"allow": ["Bash(go test:*)"], "deny": ["Read(.env)", "Edit(**/*.go)"]}}`)
	write(t, root, ".mcp.json", `{"mcpServers": {"other": {"command": "x"}}}`)
	write(t, root, ".gitignore", "node_modules")
	write(t, root, ".foreman/workspace.yaml", "services: []\n")

	rep, err := Init(Options{Root: root, Extensions: []string{".go", ".py"}, Permissions: true, MCP: true})
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	json.Unmarshal([]byte(read(t, root, ".claude/settings.json")), &settings)
	if settings["model"] != "opus" {
		t.Error("unrelated settings lost")
	}
	perms := settings["permissions"].(map[string]any)
	if len(perms["allow"].([]any)) != 1 {
		t.Error("allow rules lost")
	}
	deny, _ := json.Marshal(perms["deny"])
	if string(deny) != `["Read(.env)","Edit(**/*.go)","Edit(**/*.py)"]` {
		t.Errorf("deny = %s", deny)
	}
	if !strings.Contains(read(t, root, ".mcp.json"), `"other"`) {
		t.Error("other MCP server lost")
	}
	if read(t, root, ".gitignore") != "node_modules\n# foreman-code-ops runtime files\n.foreman/cache/\n" {
		t.Errorf(".gitignore = %q", read(t, root, ".gitignore"))
	}
	if read(t, root, ".foreman/workspace.yaml") != "services: []\n" {
		t.Error("workspace.yaml overwritten without --force")
	}
	if !strings.Contains(strings.Join(rep.Lines, "\n"), "kept .foreman/workspace.yaml") {
		t.Errorf("report = %v", rep.Lines)
	}

	// Idempotent.
	before := read(t, root, ".claude/settings.json") + read(t, root, ".gitignore") + read(t, root, ".mcp.json")
	if _, err := Init(Options{Root: root, Extensions: []string{".go", ".py"}, Permissions: true, MCP: true}); err != nil {
		t.Fatal(err)
	}
	if after := read(t, root, ".claude/settings.json") + read(t, root, ".gitignore") + read(t, root, ".mcp.json"); after != before {
		t.Error("second init changed files")
	}
}

func TestInitRejectsBrokenSettings(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/settings.json", `{broken`)
	if _, err := Init(Options{Root: root, Extensions: []string{".go"}, Permissions: true}); err == nil {
		t.Fatal("expected error")
	}
	if read(t, root, ".claude/settings.json") != `{broken` {
		t.Fatal("broken settings overwritten")
	}
}
