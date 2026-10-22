package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/journal"
	tu "github.com/MarshMeg/foreman-code-ops/internal/testutil"
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
	want := []string{"check", "create_file", "delete_file", "delete_symbol", "discover_links", "get_links", "get_symbol",
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

func TestStyleTools(t *testing.T) {
	dir := tu.RepoFromFixture(t, "../../testdata/go-layered")
	cs := connectProject(t, dir)

	if st := decode[StyleOutput](t, call(t, cs, "style_get", map[string]any{})); st.Exists {
		t.Fatal("style should not exist yet")
	}
	set := decode[ChangeOutput](t, call(t, cs, "style_set", map[string]any{"content": "# Style\n\n## Go\nWrap errors.\n"}))
	if set.Status != "applied" || set.Commit == "" {
		t.Fatalf("style_set = %+v", set)
	}
	set = decode[ChangeOutput](t, call(t, cs, "style_set", map[string]any{"section": "Logging", "content": "Use slog."}))
	if set.Status != "applied" {
		t.Fatalf("style_set section = %+v", set)
	}
	st := decode[StyleOutput](t, call(t, cs, "style_get", map[string]any{}))
	if !st.Exists || strings.Join(st.Sections, ",") != "Go,Logging" || !strings.Contains(st.Content, "Use slog.") {
		t.Fatalf("style_get = %+v", st)
	}
	if got := tu.Git(t, dir, "log", "-1", "--format=%s"); got != "foreman: style: update section Logging\n" {
		t.Fatalf("subject = %q", got)
	}
}

func TestToolErrorsAreJournaled(t *testing.T) {
	dir := tu.RepoFromFixture(t, "../../testdata/go-layered")
	var buf bytes.Buffer
	cs := connectProjectWith(t, dir, journal.New(&buf))
	res := call(t, cs, "get_symbol", map[string]any{"file": "../outside.go", "symbol": "X"})
	if !res.IsError {
		t.Fatal("expected tool error")
	}
	var e journal.Entry
	if err := json.Unmarshal(buf.Bytes(), &e); err != nil {
		t.Fatalf("journal %q: %v", buf.String(), err)
	}
	if e.Tool != "get_symbol" || e.Status != "error" || !strings.Contains(e.Error, "escapes") {
		t.Fatalf("journal entry %+v", e)
	}
}
