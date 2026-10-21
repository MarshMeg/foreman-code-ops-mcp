// Package testutil has helpers shared by integration tests.
package testutil

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// IsolateGit keeps the user's git configuration (signing, hooks, identity) out of tests.
func IsolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

// Git runs git in dir and fails the test on error.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// RepoFromFixture copies the fixture directory into a temp dir, makes it a git repository
// with one commit and returns its path.
func RepoFromFixture(t *testing.T, fixture string) string {
	t.Helper()
	IsolateGit(t)
	dst := filepath.Join(t.TempDir(), filepath.Base(fixture))
	err := filepath.WalkDir(fixture, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fixture, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	Git(t, dst, "init", "-q", "-b", "main")
	Git(t, dst, "add", ".")
	Git(t, dst, "commit", "-q", "-m", "fixture")
	return dst
}

// WriteFile writes rel under dir, creating directories.
func WriteFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ReadFile returns the content of rel under dir ("" if missing).
func ReadFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, rel))
	return string(b)
}
