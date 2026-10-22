package graph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
)

// fakeBin writes an executable shell script named name into a temp dir on PATH.
func fakeBin(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestCodebaseMemory(t *testing.T) {
	root := t.TempDir()
	logf := filepath.Join(t.TempDir(), "calls.log")
	fakeBin(t, "codebase-memory-mcp", `
echo "$@" >> `+logf+`
cat >> `+logf+`
echo >> `+logf+`
case "$2" in
  list_projects) echo '{"projects":[{"name":"other","root_path":"/elsewhere"},{"name":"books","root_path":"`+root+`"}]}' ;;
  trace_path) echo '{"function":"List","callers":[{"name":"List","file_path":"internal/service/book.go","line":24},{"qualified_name":"books.transport.BookHandler.List","file_path":"`+root+`/internal/transport/http.go","start_line":17}]}' ;;
  index_repository) echo '{"state":"succeeded"}' ;;
esac`)
	p := New(config.Graph{Provider: "codebase-memory"}, root, nil).(*codebaseMemory)
	ctx := context.Background()
	if ok, why := p.Available(ctx); !ok {
		t.Fatal(why)
	}
	refs, err := p.References(ctx, "internal/service/book.go", "BookService.List")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].File != "internal/transport/http.go" || refs[0].Line != 17 {
		t.Fatalf("refs = %+v", refs)
	}
	p.Reindex(ctx, []string{"a.go"})
	p.bg.wait()
	log, _ := os.ReadFile(logf)
	for _, want := range []string{"cli trace_path --format json --quiet", `"direction":"inbound"`, `"function_name":"List"`, `"project":"books"`,
		"cli index_repository", `"repo_path":"` + root + `"`} {
		if !strings.Contains(string(log), want) {
			t.Errorf("calls lack %q:\n%s", want, log)
		}
	}
}

func TestCodebaseMemoryNotIndexed(t *testing.T) {
	root := t.TempDir()
	fakeBin(t, "codebase-memory-mcp", `echo '{"projects":[]}'`)
	p := New(config.Graph{Provider: "codebase-memory"}, root, nil)
	if ok, why := p.Available(context.Background()); ok || !strings.Contains(why, "not indexed") {
		t.Fatalf("available=%v why=%s", ok, why)
	}
	if _, err := p.References(context.Background(), "a.go", "F"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestCodebaseMemoryAutoIndexSkipsReindex(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "called")
	fakeBin(t, "codebase-memory-mcp", "touch "+marker)
	p := New(config.Graph{Provider: "codebase-memory", AutoIndex: true, Project: "x"}, root, nil).(*codebaseMemory)
	p.Reindex(context.Background(), nil)
	p.bg.wait()
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("reindexed despite auto_index")
	}
}

func TestGraphify(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "graphify-out"), 0o755)
	os.WriteFile(filepath.Join(root, "graphify-out", "graph.json"), []byte(`{
  "directed": true, "graph": {"schema_version": 1},
  "nodes": [
    {"id": "svc_list", "label": "BookService.List()", "file_type": "code", "source_file": "internal/service/book.go"},
    {"id": "h_list", "label": "BookHandler.List()", "file_type": "code", "source_file": "internal/transport/http.go"},
    {"id": "count", "label": "BookService.Count()", "file_type": "code", "source_file": "internal/service/book.go"},
    {"id": "svc", "label": "BookService", "file_type": "code", "source_file": "internal/service/book.go"},
    {"id": "other_list", "label": "memRepo.List()", "file_type": "code", "source_file": "internal/repo/book.go"}
  ],
  "links": [
    {"source": "h_list", "target": "svc_list", "relation": "calls", "confidence": "EXTRACTED", "source_file": "internal/transport/http.go"},
    {"source": "count", "target": "svc_list", "relation": "calls", "confidence": "INFERRED", "confidence_score": 0.8},
    {"source": "svc", "target": "svc_list", "relation": "method", "confidence": "EXTRACTED"},
    {"source": "svc_list", "target": "other_list", "relation": "calls", "confidence": "EXTRACTED"}
  ]}`), 0o644)
	p := New(config.Graph{Provider: "graphify", AutoIndex: true}, root, nil)
	if ok, why := p.Available(context.Background()); !ok {
		t.Fatal(why)
	}
	refs, err := p.References(context.Background(), "internal/service/book.go", "BookService.List")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range refs {
		got = append(got, r.String())
	}
	want := "internal/transport/http.go BookHandler.List() [calls];internal/service/book.go BookService.Count() [calls, inferred]"
	if strings.Join(got, ";") != want {
		t.Fatalf("refs = %v", got)
	}
	if _, err := p.References(context.Background(), "internal/service/book.go", "Nope"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestCommandProvider(t *testing.T) {
	root := t.TempDir()
	fakeBin(t, "myrefs", `echo "internal/a.go:12: uses $2"; echo "$3:3: other"`)
	p := New(config.Graph{Provider: "command", References: []string{"myrefs", "refs", "{name}", "{file}"}}, root, nil)
	refs, err := p.References(context.Background(), "internal/b.go", "T.Run")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].File != "internal/a.go" || refs[0].Line != 12 || refs[0].Symbol != "uses Run" {
		t.Fatalf("refs = %+v", refs)
	}
}

func TestNoop(t *testing.T) {
	p := New(config.Graph{Provider: "noop"}, t.TempDir(), nil)
	if _, err := p.References(context.Background(), "a", "b"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
