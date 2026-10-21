package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touch(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscover(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mono")
	touch(t, root,
		"backend/go.mod",
		"frontend/package.json", "frontend/tsconfig.json",
		"frontend/node_modules/dep/package.json",
		"services/worker/pyproject.toml",
		"tools/package.json",
		"other/backend/go.mod",
		".hidden/go.mod",
	)
	svcs, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range svcs {
		got = append(got, s.Name+"="+s.Root+":"+s.Lang)
	}
	want := "backend-go=backend:go frontend=frontend:typescript backend-go-2=other/backend:go worker=services/worker:python tools=tools:javascript"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %v\nwant %v", strings.Join(got, " "), want)
	}
}

func TestWorkspaceRoundTripAndServiceFor(t *testing.T) {
	root := t.TempDir()
	w := Workspace{Services: []Service{
		{Name: "root", Root: ".", Lang: "go"},
		{Name: "web", Root: "web", Lang: "typescript", Consumes: []Consume{{Service: "root", BaseURLEnv: "API"}}},
		{Name: "web-admin", Root: "web/admin", Lang: "typescript"},
	}}
	data, err := MarshalWorkspace(w)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(WorkspacePath(root)), 0o755)
	os.WriteFile(WorkspacePath(root), data, 0o644)

	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Workspace.Services) != 3 || p.Workspace.Services[1].Consumes[0].BaseURLEnv != "API" {
		t.Fatalf("loaded %+v", p.Workspace)
	}
	for rel, want := range map[string]string{
		"main.go":           "root",
		"web/src/a.ts":      "web",
		"web/admin/x.ts":    "web-admin",
		"web/administer.ts": "web",
	} {
		if s := p.ServiceFor(rel); s == nil || s.Name != want {
			t.Errorf("ServiceFor(%s) = %v, want %s", rel, s, want)
		}
	}
	if got := p.ServiceNames([]string{"web/a.ts", "main.go", "web/b.ts"}); strings.Join(got, ",") != "root,web" {
		t.Errorf("ServiceNames = %v", got)
	}
}

func TestOpenWithoutWorkspace(t *testing.T) {
	p, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p.ServiceFor("a.go") != nil {
		t.Fatal("expected no service")
	}
}

func TestWorkspaceValidation(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Dir(WorkspacePath(root)), 0o755)
	os.WriteFile(WorkspacePath(root), []byte("services:\n  - {name: a, root: ../x}\n  - {name: a, root: b}\n"), 0o644)
	_, err := Open(root)
	if err == nil || !strings.Contains(err.Error(), "escapes") || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolve(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	touch(t, root, "pkg/a.go")
	os.Symlink(outside, filepath.Join(root, "link"))
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, ok := range []string{"pkg/a.go", "pkg/new/b.go", "./pkg/../pkg/a.go"} {
		rel, abs, err := p.Resolve(ok)
		if err != nil {
			t.Errorf("Resolve(%s): %v", ok, err)
			continue
		}
		if abs != filepath.Join(p.Root, rel) {
			t.Errorf("abs = %s", abs)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "pkg/../../x", ".git/config", "a/.git/x", "link/x.go", "."} {
		if _, _, err := p.Resolve(bad); err == nil {
			t.Errorf("Resolve(%q) succeeded", bad)
		}
	}
}
