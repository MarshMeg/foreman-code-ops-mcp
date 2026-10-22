package gate

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/workspace"
)

// maxCopiedConfig is the largest top-level file copied (not symlinked) into a shadow root.
const maxCopiedConfig = 1 << 20

// env is where commands of one root run for one changeset: the real root with an overlay
// file (Go), or a shadow root holding the candidate files (tools that read the disk).
type env struct {
	g       *Gate
	tc      lang.Toolchain
	root    string // project-relative
	realDir string // absolute real root
	dir     string // absolute directory commands run in
	tmp     string // scratch directory, also holds overlay.json
	overlay string
	// backing maps files tools may report (overlay backing files) to project-relative paths.
	backing map[string]string
	// shadow is the shadow root, mapped back to realDir in tool output; "" when unused.
	shadow  string
	release func()
}

// newEnv prepares an environment for adapter a in root.
func (g *Gate) newEnv(a lang.Adapter, root string, cs *workspace.Changeset) (*env, error) {
	tc := a.Toolchain()
	tmp, err := os.MkdirTemp("", "foreman-gate-")
	if err != nil {
		return nil, err
	}
	// Tools report resolved paths (macOS /var -> /private/var); match them.
	if real, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = real
	}
	e := &env{g: g, tc: tc, root: cleanRoot(root), tmp: tmp, overlay: filepath.Join(tmp, "overlay.json"), backing: map[string]string{}}
	e.realDir = filepath.Join(g.proj.Root, filepath.FromSlash(e.root))
	e.dir = e.realDir
	e.release = func() { os.RemoveAll(tmp) }

	if err := e.writeOverlay(cs); err != nil {
		e.close()
		return nil, err
	}
	if !tc.Overlay && touchesRoot(cs, e.root) {
		shadow, unlock, err := g.buildShadow(e.root, e.realDir, cs)
		if err != nil {
			e.close()
			return nil, err
		}
		e.dir, e.shadow = shadow, shadow
		prev := e.release
		e.release = func() { unlock(); prev() }
	}
	return e, nil
}

func (e *env) close() { e.release() }

func (e *env) writeOverlay(cs *workspace.Changeset) error {
	replace := map[string]string{}
	for i, rel := range cs.Files() {
		content, exists, err := cs.Read(rel)
		if err != nil {
			return err
		}
		abs := filepath.Join(e.g.proj.Root, filepath.FromSlash(rel))
		if !exists {
			replace[abs] = ""
			continue
		}
		backing := filepath.Join(e.tmp, fmt.Sprintf("f%d%s", i, filepath.Ext(rel)))
		if err := os.WriteFile(backing, content, 0o644); err != nil {
			return err
		}
		replace[abs] = backing
		e.backing[backing] = rel
	}
	data, _ := json.Marshal(map[string]any{"Replace": replace})
	return os.WriteFile(e.overlay, data, 0o644)
}

func touchesRoot(cs *workspace.Changeset, root string) bool {
	for _, rel := range cs.Files() {
		if root == "." || rel == root || strings.HasPrefix(rel, root+"/") {
			return true
		}
	}
	return false
}

// shadowLocks serializes use of each shadow root (one per project root).
var shadowLocks sync.Map // string -> *sync.Mutex

// heavyDirs are symlinked into shadow roots instead of copied.
var heavyDirs = map[string]bool{
	"node_modules": true, ".git": true, ".venv": true, "venv": true, "__pycache__": true,
	"dist": true, "build": true, "target": true, ".next": true, "coverage": true,
	".mypy_cache": true, ".ruff_cache": true, ".pytest_cache": true, ".foreman": true,
}

// buildShadow syncs a stable copy of realDir and writes the candidate files into it. Files
// are copied, not linked: Node and Python resolve symlinks to real paths, which would make
// tests import the old code, and hard links would let a tool write through to the project.
// Unchanged files are skipped by size and mtime, so repeated checks are cheap. Heavy
// directories (node_modules, .git, virtualenvs) are symlinked. The location is stable so
// incremental caches (tsc) stay useful, and outside the project so tools never see it.
func (g *Gate) buildShadow(root, realDir string, cs *workspace.Changeset) (string, func(), error) {
	sum := sha1.Sum([]byte(g.proj.Root + "\x00" + root))
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "foreman-code-ops", "shadow", hex.EncodeToString(sum[:8]))
	m, _ := shadowLocks.LoadOrStore(dir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	fail := func(err error) (string, func(), error) { mu.Unlock(); return "", nil, err }

	if err := syncTree(realDir, dir); err != nil {
		return fail(err)
	}
	for _, rel := range cs.Files() {
		if root != "." && rel != root && !strings.HasPrefix(rel, root+"/") {
			continue
		}
		content, exists, err := cs.Read(rel)
		if err != nil {
			return fail(err)
		}
		p := filepath.Join(dir, filepath.FromSlash(relTo(root, rel)))
		if !exists {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fail(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fail(err)
		}
		// A fresh mtime makes the next sync restore the real content.
		if err := os.WriteFile(p, content, 0o644); err != nil {
			return fail(err)
		}
		now := time.Now().Add(time.Second)
		os.Chtimes(p, now, now)
	}
	return dir, mu.Unlock, nil
}

// syncTree makes dst mirror src: copies new or changed files, removes stale entries and
// symlinks heavy directories.
func syncTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		want[name] = true
		sp, dp := filepath.Join(src, name), filepath.Join(dst, name)
		di, derr := os.Lstat(dp)
		switch {
		case e.IsDir() && heavyDirs[name], e.Type()&fs.ModeSymlink != 0:
			if derr == nil && di.Mode()&fs.ModeSymlink != 0 {
				if t, _ := os.Readlink(dp); t == sp {
					continue
				}
			}
			os.RemoveAll(dp)
			if err := os.Symlink(sp, dp); err != nil {
				return err
			}
		case e.IsDir():
			if derr == nil && !di.IsDir() {
				os.RemoveAll(dp)
			}
			if err := syncTree(sp, dp); err != nil {
				return err
			}
		case e.Type().IsRegular():
			si, err := e.Info()
			if err != nil {
				return err
			}
			if derr == nil && di.Mode().IsRegular() && di.Size() == si.Size() && di.ModTime().Equal(si.ModTime()) {
				continue
			}
			if derr == nil && !di.Mode().IsRegular() {
				os.RemoveAll(dp)
			}
			if err := copyFile(sp, dp, si); err != nil {
				return err
			}
		}
	}
	existing, _ := os.ReadDir(dst)
	for _, e := range existing {
		if !want[e.Name()] {
			os.RemoveAll(filepath.Join(dst, e.Name()))
		}
	}
	return nil
}

func copyFile(src, dst string, info fs.FileInfo) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, b, info.Mode().Perm()|0o200); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

// cacheDir is a stable per-root directory for tool caches ({cache}), separate for shadow runs
// because tools record absolute paths.
func (e *env) cacheDir() string {
	key := e.g.proj.Root + "\x00" + e.root
	if e.shadow != "" {
		key += "\x00shadow"
	}
	sum := sha1.Sum([]byte(key))
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "foreman-code-ops", "cache", hex.EncodeToString(sum[:8]))
	os.MkdirAll(dir, 0o755)
	return dir
}

// resolve finds a command: the toolchain's bin dirs under the real root first, then PATH.
func (e *env) resolve(name string) (string, bool) {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name, true
	}
	for _, d := range e.tc.BinDirs {
		p := filepath.Join(e.realDir, filepath.FromSlash(d), name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p, true
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	return name, false
}

// applies evaluates a step condition against the real root.
func (e *env) applies(c lang.Condition) bool {
	exists := func(f string) bool {
		_, err := os.Stat(filepath.Join(e.realDir, f))
		return err == nil
	}
	if len(c.Files) > 0 && !anyOf(c.Files, exists) {
		return false
	}
	if len(c.NoFiles) > 0 && anyOf(c.NoFiles, exists) {
		return false
	}
	if len(c.Deps) > 0 && !anyOf(c.Deps, e.hasDep) {
		return false
	}
	if len(c.Contains) > 0 {
		found := false
		for f, sub := range c.Contains {
			if b, err := os.ReadFile(filepath.Join(e.realDir, f)); err == nil && bytes.Contains(b, []byte(sub)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func anyOf(xs []string, f func(string) bool) bool {
	for _, x := range xs {
		if f(x) {
			return true
		}
	}
	return false
}

func (e *env) hasDep(name string) bool {
	b, err := os.ReadFile(filepath.Join(e.realDir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Deps    map[string]any `json:"dependencies"`
		DevDeps map[string]any `json:"devDependencies"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return false
	}
	_, a := pkg.Deps[name]
	_, d := pkg.DevDeps[name]
	return a || d
}

// selector picks the steps to run: applicable, installed (or not optional), first per group.
type selector struct {
	e      *env
	groups map[string]bool
}

func (e *env) selector() *selector { return &selector{e: e, groups: map[string]bool{}} }

// pick returns the resolved argv[0] and whether the step runs. A missing non-optional
// command still runs, so it reports "not found".
func (s *selector) pick(st lang.Step) (string, bool) {
	if st.Group != "" && s.groups[st.Group] {
		return "", false
	}
	if !s.e.applies(st.When) {
		return "", false
	}
	cmd := ""
	if len(st.Run) > 0 {
		var found bool
		cmd, found = s.e.resolve(st.Run[0])
		if !found && (st.Optional || st.Group != "") {
			return "", false
		}
	}
	if st.Group != "" {
		s.groups[st.Group] = true
	}
	return cmd, true
}

type execResult struct {
	stdout, stderr string
	exitCode       int
	// failure is set when the command could not run or timed out.
	failure *Diagnostic
}

func (d *Diagnostic) withSource(s string) *Diagnostic {
	d.Source = s
	return d
}

func (e *env) exec(ctx context.Context, args []string, stdin []byte, timeout time.Duration) execResult {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = e.dir
	// NO_COLOR only: some tools treat any FORCE_COLOR value as "force color".
	cmd.Env = append(withoutEnv(os.Environ(), "FORCE_COLOR", "CLICOLOR_FORCE"), "NO_COLOR=1")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := execResult{stdout: stdout.String(), stderr: stderr.String()}
	var ee *exec.ExitError
	name := filepath.Base(args[0])
	switch {
	case err == nil:
	case ctx.Err() == context.DeadlineExceeded:
		res.failure = &Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("%s timed out after %s", name, timeout)}
	case errors.As(err, &ee):
		res.exitCode = ee.ExitCode()
	case errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist):
		res.failure = &Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("%s not found in PATH", name)}
	default:
		res.failure = &Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("run %s: %v", name, err)}
	}
	return res
}

// parser builds the output parser for a step run in this environment.
func (e *env) parser(s lang.Step) outputParser {
	return outputParser{
		re:          e.g.res[s.Pattern],
		source:      stepName(s),
		severity:    severity(s),
		onlyMatches: s.OnlyMatches,
		format:      s.Parser,
		projectRoot: e.g.proj.Root,
		cwd:         e.dir,
		backing:     e.backing,
		shadow:      e.shadow,
		realDir:     e.realDir,
	}
}

// unshadow rewrites shadow paths in free text (test output) to project-relative ones.
func (e *env) unshadow(s string) string {
	for backing, rel := range e.backing {
		s = strings.ReplaceAll(s, backing, rel)
	}
	if e.shadow != "" {
		s = strings.ReplaceAll(s, e.shadow+string(os.PathSeparator), relDirPrefix(e.root))
		s = strings.ReplaceAll(s, e.shadow, e.root)
	}
	return s
}

func relDirPrefix(root string) string {
	if root == "." {
		return ""
	}
	return root + "/"
}

func withoutEnv(env []string, keys ...string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, key := range keys {
			if k == key {
				drop = true
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}
