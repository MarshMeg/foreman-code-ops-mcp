package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func read(t *testing.T, root, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

func TestApplyAndDiff(t *testing.T) {
	root := setup(t, map[string]string{"a.go": "one\ntwo\n", "gone.go": "x\n", "same.go": "s\n"})
	c := New(root)
	c.Write("a.go", []byte("one\nTWO\n"))
	c.Write("dir/new.go", []byte("new\n"))
	c.Delete("gone.go")
	c.Write("same.go", []byte("s\n"))

	if got := strings.Join(c.Files(), ","); got != "a.go,dir/new.go,gone.go" {
		t.Fatalf("files = %s", got)
	}
	d := c.Diff()
	for _, want := range []string{"--- a/a.go\n+++ b/a.go", "-two\n+TWO\n", "--- /dev/null\n+++ b/dir/new.go", "+new\n", "--- a/gone.go\n+++ /dev/null", "-x\n"} {
		if !strings.Contains(d, want) {
			t.Errorf("diff missing %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "same.go") {
		t.Errorf("unchanged file in diff")
	}

	if err := c.Apply(); err != nil {
		t.Fatal(err)
	}
	if s, _ := read(t, root, "a.go"); s != "one\nTWO\n" {
		t.Errorf("a.go = %q", s)
	}
	if s, _ := read(t, root, "dir/new.go"); s != "new\n" {
		t.Errorf("new.go = %q", s)
	}
	if _, ok := read(t, root, "gone.go"); ok {
		t.Error("gone.go still exists")
	}
	info, _ := os.Stat(filepath.Join(root, "a.go"))
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want preserved 0640", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

func TestReadSeesStagedContent(t *testing.T) {
	root := setup(t, map[string]string{"a.go": "old"})
	c := New(root)
	c.Write("a.go", []byte("new"))
	b, ok, _ := c.Read("a.go")
	if !ok || string(b) != "new" {
		t.Fatalf("got %q %v", b, ok)
	}
	c.Delete("a.go")
	if _, ok, _ := c.Read("a.go"); ok {
		t.Fatal("deleted file reported as existing")
	}
	if _, ok, _ := c.Read("missing.go"); ok {
		t.Fatal("missing file reported as existing")
	}
}

func TestApplyRollsBack(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := setup(t, map[string]string{"a.go": "orig\n", "z/b.go": "b\n"})
	c := New(root)
	c.Write("a.go", []byte("changed\n"))
	c.Write("z/b.go", []byte("cannot be written\n"))
	ro := filepath.Join(root, "z")
	os.Chmod(ro, 0o555)
	t.Cleanup(func() { os.Chmod(ro, 0o755) })

	if err := c.Apply(); err == nil {
		t.Fatal("expected error")
	}
	if s, _ := read(t, root, "a.go"); s != "orig\n" {
		t.Fatalf("a.go not rolled back: %q", s)
	}
}

func TestRejectsDirectory(t *testing.T) {
	root := setup(t, map[string]string{"d/x": ""})
	if err := New(root).Write("d", []byte("x")); err == nil {
		t.Fatal("expected error for directory")
	}
}
