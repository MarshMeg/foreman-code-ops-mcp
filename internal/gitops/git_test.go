package gitops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// isolateGit keeps the user's git config (signing, hooks, identity) out of tests.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	isolateGit(t)
	dir := t.TempDir()
	sh(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.go", "a\n")
	write(t, dir, "b.go", "b\n")
	write(t, dir, "sub/c.go", "c\n")
	sh(t, dir, "add", ".")
	sh(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestOpenNotRepo(t *testing.T) {
	isolateGit(t)
	if _, err := Open(context.Background(), t.TempDir()); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("err = %v", err)
	}
}

func TestDirty(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "a.go", "changed\n")
	write(t, dir, "untracked.go", "u\n")
	os.Remove(filepath.Join(dir, "sub/c.go"))
	got, err := r.Dirty(ctx, []string{"a.go", "b.go", "untracked.go", "sub/c.go", "new.go"})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if strings.Join(got, ",") != "a.go,sub/c.go,untracked.go" {
		t.Fatalf("dirty = %v", got)
	}
}

func TestDirtyFromSubdirectory(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r, _ := Open(ctx, filepath.Join(dir, "sub"))
	write(t, dir, "sub/c.go", "changed\n")
	got, err := r.Dirty(ctx, []string{"c.go"})
	if err != nil || strings.Join(got, ",") != "c.go" {
		t.Fatalf("dirty = %v, %v", got, err)
	}
}

func TestCommitOnlyTouchedFiles(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r, _ := Open(ctx, dir)

	// The user has an unrelated staged change; it must stay staged and uncommitted.
	write(t, dir, "b.go", "user change\n")
	sh(t, dir, "add", "b.go")

	write(t, dir, "a.go", "foreman change\n")
	write(t, dir, "new/n.go", "n\n")
	os.Remove(filepath.Join(dir, "sub/c.go"))

	msg := Message{Services: []string{"backend"}, Subject: "upsert a", Body: "spec: do things",
		Trailers: []Trailer{{TrailerWrittenBy, "local (m)"}, {TrailerAttempts, "build=1 review=0"}}}
	hash, err := r.Commit(ctx, []string{"a.go", "new/n.go", "sub/c.go"}, msg)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Fatal("no hash")
	}
	files := sh(t, dir, "show", "--name-status", "--format=", "HEAD")
	if files != "M\ta.go\nA\tnew/n.go\nD\tsub/c.go\n" {
		t.Fatalf("committed files:\n%s", files)
	}
	if staged := sh(t, dir, "diff", "--cached", "--name-only"); staged != "b.go\n" {
		t.Fatalf("user's staged change lost: %q", staged)
	}
	body := sh(t, dir, "log", "-1", "--format=%B")
	if !strings.HasPrefix(body, "foreman(backend): upsert a\n\nspec: do things\n\nForeman-Written-By: local (m)\n") {
		t.Fatalf("message:\n%s", body)
	}
	trailers := sh(t, dir, "log", "-1", "--format=%(trailers:key=Foreman-Attempts,valueonly)")
	if strings.TrimSpace(trailers) != "build=1 review=0" {
		t.Fatalf("trailer = %q", trailers)
	}
}

func TestCommitHookFailureKeepsFiles(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r, _ := Open(ctx, dir)
	write(t, dir, ".git/hooks/pre-commit", "#!/bin/sh\necho 'lint failed' >&2\nexit 1\n")
	os.Chmod(filepath.Join(dir, ".git/hooks/pre-commit"), 0o755)

	write(t, dir, "a.go", "changed\n")
	_, err := r.Commit(ctx, []string{"a.go"}, Message{Subject: "x"})
	var ce *CommandError
	if !errors.As(err, &ce) || !strings.Contains(ce.Output, "lint failed") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.go")); string(b) != "changed\n" {
		t.Fatal("file was reverted")
	}
}

func TestEnsureBranch(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r, _ := Open(ctx, dir)
	if err := r.EnsureBranch(ctx, "foreman/s1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := r.CurrentBranch(ctx); b != "foreman/s1" {
		t.Fatalf("branch = %s", b)
	}
	sh(t, dir, "switch", "-q", "main")
	if err := r.EnsureBranch(ctx, "foreman/s1"); err != nil { // existing branch
		t.Fatal(err)
	}
	if b, _ := r.CurrentBranch(ctx); b != "foreman/s1" {
		t.Fatalf("branch = %s", b)
	}
}

func TestGrep(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	write(t, dir, "sub/use.go", "x := List(1)\nListAll()\n")
	write(t, dir, ".gitignore", "ignored/\n")
	write(t, dir, "ignored/z.go", "List\n")
	r, _ := Open(ctx, filepath.Join(dir, "sub"))
	m, err := r.Grep(ctx, "List")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m[0].Path != "use.go" || m[0].Line != 1 || m[0].Text != "x := List(1)" {
		t.Fatalf("matches = %+v", m)
	}
	if m, err := r.Grep(ctx, "Nothing"); err != nil || len(m) != 0 {
		t.Fatalf("no-match: %v %v", m, err)
	}
}

func TestMessageWithoutExtras(t *testing.T) {
	if got := (Message{Subject: "delete  a.go\nnow"}).String(); got != "foreman: delete a.go now\n" {
		t.Fatalf("got %q", got)
	}
}

func TestDirtyIgnoresStderrWarnings(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r, _ := Open(ctx, dir)
	got, err := r.Dirty(ctx, []string{"missing/dir/x.go"})
	if err != nil || len(got) != 0 {
		t.Fatalf("dirty = %q, %v", got, err)
	}
}
