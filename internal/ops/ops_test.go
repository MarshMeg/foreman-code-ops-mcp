package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/lang"
	"github.com/MarshMeg/foreman-code-ops/internal/lang/builtin"
	"github.com/MarshMeg/foreman-code-ops/internal/project"
	tu "github.com/MarshMeg/foreman-code-ops/internal/testutil"
)

const fixture = "../../testdata/go-layered"

func newOps(t *testing.T, dir string, mutate ...func(*config.Config)) *Ops {
	t.Helper()
	cfg := config.Default()
	for _, m := range mutate {
		m(&cfg)
	}
	proj, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := builtin.Registry()
	if err != nil {
		t.Fatal(err)
	}
	return New(proj, reg, cfg, nil)
}

func headFiles(t *testing.T, dir string) string {
	return tu.Git(t, dir, "show", "--name-status", "--format=%s", "HEAD")
}

func TestGetSymbol(t *testing.T) {
	dir := tu.RepoFromFixture(t, fixture)
	o := newOps(t, dir)
	info, err := o.GetSymbol("./internal/service/book.go", "BookService.Count")
	if err != nil {
		t.Fatal(err)
	}
	if info.File != "internal/service/book.go" || !strings.HasPrefix(info.Source, "// Count returns") || info.Symbol.Kind != "method" {
		t.Fatalf("got %+v", info)
	}
	if _, err := o.GetSymbol("internal/service/book.go", "Nope"); !errors.Is(err, lang.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := o.GetSymbol("go.mod", "x"); err == nil || !strings.Contains(err.Error(), "no language adapter") {
		t.Fatalf("err = %v", err)
	}
	if _, err := o.GetSymbol("../etc/passwd", "x"); err == nil {
		t.Fatal("escape allowed")
	}
}

func TestDeleteSymbolRefusesWhenReferenced(t *testing.T) {
	dir := tu.RepoFromFixture(t, fixture)
	o := newOps(t, dir)
	before := tu.ReadFile(t, dir, "internal/service/book.go")

	res, err := o.DeleteSymbol(context.Background(), "internal/service/book.go", "BookService", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusRefused || res.Commit != "" || len(res.Warnings) == 0 {
		t.Fatalf("got %+v", res)
	}
	refs := strings.Join(res.References, "\n")
	if !strings.Contains(refs, "internal/transport/http.go:13: svc *service.BookService") {
		t.Fatalf("refs = %v", res.References)
	}
	// Lines 12-15 are the symbol itself (doc comment and declaration).
	for _, r := range res.References {
		for _, own := range []string{":12:", ":13:"} {
			if strings.HasPrefix(r, "internal/service/book.go"+own) {
				t.Fatalf("own definition reported: %s", r)
			}
		}
	}
	if tu.ReadFile(t, dir, "internal/service/book.go") != before {
		t.Fatal("file changed on refusal")
	}
}

func TestDeleteSymbolUnreferenced(t *testing.T) {
	dir := tu.RepoFromFixture(t, fixture)
	o := newOps(t, dir)
	res, err := o.DeleteSymbol(context.Background(), "internal/service/book.go", "unusedHelper", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusApplied || res.Commit == "" || !strings.Contains(res.Diff, "-func unusedHelper() {}") {
		t.Fatalf("got %+v", res)
	}
	src := tu.ReadFile(t, dir, "internal/service/book.go")
	if strings.Contains(src, "unusedHelper") || !strings.HasSuffix(src, "return len(books), err\n}\n") {
		t.Fatalf("source:\n%s", src)
	}
	if got := headFiles(t, dir); got != "foreman: delete internal/service/book.go unusedHelper\n\nM\tinternal/service/book.go\n" {
		t.Fatalf("HEAD = %q", got)
	}
}

func TestDeleteSymbolForce(t *testing.T) {
	dir := tu.RepoFromFixture(t, fixture)
	o := newOps(t, dir)
	res, err := o.DeleteSymbol(context.Background(), "internal/service/book.go", "BookService.Count", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusApplied {
		t.Fatalf("got %+v", res)
	}
	if strings.Contains(tu.ReadFile(t, dir, "internal/service/book.go"), "Count") {
		t.Fatal("Count still present")
	}
}

func TestDirtyRefuseAndInclude(t *testing.T) {
	dir := tu.RepoFromFixture(t, fixture)
	tu.WriteFile(t, dir, "internal/service/book.go", tu.ReadFile(t, dir, "internal/service/book.go")+"\n// user edit\n")

	res, err := newOps(t, dir).DeleteSymbol(context.Background(), "internal/service/book.go", "unusedHelper", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusRefused || !strings.Contains(res.Message, "uncommitted") {
		t.Fatalf("got %+v", res)
	}
	if !strings.Contains(tu.ReadFile(t, dir, "internal/service/book.go"), "unusedHelper") {
		t.Fatal("file changed despite refusal")
	}

	res, err = newOps(t, dir, func(c *config.Config) { c.Git.Dirty = "include" }).
		DeleteSymbol(context.Background(), "internal/service/book.go", "unusedHelper", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusApplied || len(res.Warnings) == 0 {
		t.Fatalf("got %+v", res)
	}
}

func TestCreateAndDeleteFile(t *testing.T) {
	dir := tu.RepoFromFixture(t, fixture)
	o := newOps(t, dir)
	ctx := context.Background()

	res, err := o.CreateFile(ctx, "internal/service/author.go", "")
	if err != nil || res.Status != StatusApplied {
		t.Fatalf("create: %+v %v", res, err)
	}
	if got := tu.ReadFile(t, dir, "internal/service/author.go"); got != "package service\n" {
		t.Fatalf("content = %q", got)
	}
	if _, err := o.CreateFile(ctx, "internal/service/author.go", ""); err == nil {
		t.Fatal("create over existing file succeeded")
	}

	res, _ = o.CreateFile(ctx, "internal/newpkg/x.go", "")
	if got := tu.ReadFile(t, dir, "internal/newpkg/x.go"); res.Status != StatusApplied || got != "package newpkg\n" {
		t.Fatalf("new pkg: %+v %q", res, got)
	}
	res, _ = o.CreateFile(ctx, "docs/notes.txt", "")
	if res.Status != StatusApplied {
		t.Fatalf("plain file: %+v", res)
	}

	res, err = o.DeleteFile(ctx, "internal/service/author.go")
	if err != nil || res.Status != StatusApplied {
		t.Fatalf("delete: %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal/service/author.go")); !os.IsNotExist(err) {
		t.Fatal("file still exists")
	}
	if got := headFiles(t, dir); got != "foreman: delete internal/service/author.go\n\nD\tinternal/service/author.go\n" {
		t.Fatalf("HEAD = %q", got)
	}
	if _, err := o.DeleteFile(ctx, "internal/service/author.go"); err == nil {
		t.Fatal("deleting a missing file succeeded")
	}
}

func TestServicesInCommitSubject(t *testing.T) {
	dir := tu.RepoFromFixture(t, fixture)
	tu.WriteFile(t, dir, ".foreman/workspace.yaml", "services:\n  - {name: books, root: ., lang: go}\n  - {name: svc, root: internal/service, lang: go}\n")
	tu.Git(t, dir, "add", ".")
	tu.Git(t, dir, "commit", "-q", "-m", "ws")
	o := newOps(t, dir)
	if _, err := o.CreateFile(context.Background(), "internal/service/x.go", ""); err != nil {
		t.Fatal(err)
	}
	if got := tu.Git(t, dir, "log", "-1", "--format=%s"); got != "foreman(svc): create internal/service/x.go\n" {
		t.Fatalf("subject = %q", got)
	}
}

func TestCommitToConfiguredBranch(t *testing.T) {
	dir := tu.RepoFromFixture(t, fixture)
	o := newOps(t, dir, func(c *config.Config) { c.Git.Branch = "foreman/test" })
	if _, err := o.CreateFile(context.Background(), "a.go", "x"); err != nil {
		t.Fatal(err)
	}
	if got := tu.Git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "foreman/test\n" {
		t.Fatalf("branch = %q", got)
	}
	if got := tu.Git(t, dir, "log", "-1", "--format=%s", "main"); got != "fixture\n" {
		t.Fatalf("main moved: %q", got)
	}
}

func TestNotARepo(t *testing.T) {
	tu.IsolateGit(t)
	dir := t.TempDir()
	tu.WriteFile(t, dir, "a.go", "package a\n\nfunc F() {}\n")
	o := newOps(t, dir)
	if _, err := o.GetSymbol("a.go", "F"); err != nil {
		t.Fatalf("get_symbol should work without git: %v", err)
	}
	if _, err := o.DeleteSymbol(context.Background(), "a.go", "F", true); err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(tu.ReadFile(t, dir, "a.go"), "func F") {
		t.Fatal("file changed without a repo")
	}
}
