package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/journal"
)

func connect(t *testing.T, d Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	if _, err := NewServer(d).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args any) *sdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func decode[T any](t *testing.T, res *sdk.CallToolResult) T {
	t.Helper()
	var v T
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestToolsListed(t *testing.T) {
	cs := connect(t, Deps{Config: config.Default()})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
	want := []string{"check", "create_file", "delete_file", "delete_symbol", "get_symbol",
		"run_tests", "status", "style_get", "style_set", "upsert_symbol"}
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestStatus(t *testing.T) {
	cfg := config.Default()
	cfg.Validator.Provider = "none"
	cs := connect(t, Deps{Version: "v0", ProjectDir: "/p", Config: cfg})
	res := call(t, cs, "status", map[string]any{})
	if res.IsError {
		t.Fatalf("status error: %+v", res.Content)
	}
	st := decode[StatusOutput](t, res)
	if st.Version != "v0" || st.Generator.Slots != 3 || st.Validator.Enabled {
		t.Fatalf("unexpected status %+v", st)
	}
	if len(st.Warnings) == 0 || !strings.Contains(strings.Join(st.Warnings, "\n"), "validator disabled") {
		t.Fatalf("warnings = %v", st.Warnings)
	}
}

func TestStyleGet(t *testing.T) {
	dir := t.TempDir()
	cs := connect(t, Deps{ProjectDir: dir, Config: config.Default()})

	if st := decode[StyleOutput](t, call(t, cs, "style_get", map[string]any{})); st.Exists {
		t.Fatal("style should not exist yet")
	}
	path := filepath.Join(dir, config.DirName, "style.md")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("## Go\nwrap errors\n"), 0o644)
	st := decode[StyleOutput](t, call(t, cs, "style_get", map[string]any{}))
	if !st.Exists || !strings.Contains(st.Content, "wrap errors") {
		t.Fatalf("got %+v", st)
	}
}

func TestStubsReturnToolErrorAndJournal(t *testing.T) {
	var buf bytes.Buffer
	cs := connect(t, Deps{Config: config.Default(), Journal: journal.New(&buf)})
	res := call(t, cs, "upsert_symbol", map[string]any{
		"file": "a.go", "symbol": "F", "kind": "function", "spec": "s", "interface": "func F()",
		"run_tests": true, "limits": map[string]any{"max_reviews": 1, "check_timeout": "10s"},
	})
	if !res.IsError {
		t.Fatal("expected tool error from stub")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "not implemented") {
		t.Fatalf("error text = %q", text)
	}
	var e journal.Entry
	if err := json.Unmarshal(buf.Bytes(), &e); err != nil {
		t.Fatalf("journal %q: %v", buf.String(), err)
	}
	if e.Tool != "upsert_symbol" || e.Status != "error" {
		t.Fatalf("journal entry %+v", e)
	}
}
