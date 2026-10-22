// Package gitops runs the git commands foreman needs: dirty checks on touched files and
// commits that contain only those files, never anything else the user has staged.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Repo runs git in a project directory (which may be a subdirectory of the work tree).
// Paths passed to its methods are relative to that directory.
type Repo struct {
	dir string
}

// ErrNotRepo means the project is not inside a git work tree.
var ErrNotRepo = errors.New("project is not inside a git work tree")

// Open checks that dir is inside a git work tree.
func Open(ctx context.Context, dir string) (*Repo, error) {
	r := &Repo{dir: dir}
	out, err := r.git(ctx, nil, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(out) != "true" {
		return nil, fmt.Errorf("%w: %s", ErrNotRepo, dir)
	}
	return r, nil
}

// CommandError is a failed git invocation with its combined output.
type CommandError struct {
	Args   []string
	Output string
	Err    error
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("git %s: %v\n%s", strings.Join(e.Args, " "), e.Err, strings.TrimSpace(e.Output))
}

func (e *CommandError) Unwrap() error { return e.Err }

func (r *Repo) git(ctx context.Context, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.dir
	// Literal pathspecs: file names are never globs.
	cmd.Env = append(os.Environ(), "GIT_LITERAL_PATHSPECS=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	// Only stdout is parsed; stderr (warnings, hook output) goes into errors.
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), &CommandError{Args: args, Output: stderr.String() + stdout.String(), Err: err}
	}
	return stdout.String(), nil
}

// Dirty returns those of paths that differ from HEAD: modified, staged, deleted or untracked.
func (r *Repo) Dirty(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	args := append([]string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--"}, paths...)
	out, err := r.git(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	var dirty []string
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		dirty = append(dirty, e[3:])
		if e[0] == 'R' || e[0] == 'C' {
			i++ // the next entry is the rename source
		}
	}
	return r.relative(ctx, dirty)
}

// relative converts work-tree-root paths from porcelain output to paths relative to r.dir.
func (r *Repo) relative(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return paths, nil
	}
	prefix, err := r.git(ctx, nil, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	prefix = strings.TrimSpace(prefix)
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = strings.TrimPrefix(p, prefix)
	}
	return out, nil
}

// CurrentBranch returns the checked-out branch name ("" when detached).
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.git(ctx, nil, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		var ce *CommandError
		if errors.As(err, &ce) && strings.TrimSpace(ce.Output) == "" {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// EnsureBranch switches to branch, creating it from HEAD if needed. Uncommitted changes
// are carried over, as with `git switch`.
func (r *Repo) EnsureBranch(ctx context.Context, branch string) error {
	cur, err := r.CurrentBranch(ctx)
	if err != nil || cur == branch {
		return err
	}
	if _, err := r.git(ctx, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err = r.git(ctx, nil, "switch", "--quiet", branch)
		return err
	}
	_, err = r.git(ctx, nil, "switch", "--quiet", "-c", branch)
	return err
}

// Commit stages exactly paths (including deletions) and commits only them, leaving any
// other staged changes of the user staged and uncommitted. Returns the short commit hash.
func (r *Repo) Commit(ctx context.Context, paths []string, msg Message) (string, error) {
	if len(paths) == 0 {
		return "", errors.New("commit: no paths")
	}
	if _, err := r.git(ctx, nil, append([]string{"add", "--all", "--"}, paths...)...); err != nil {
		return "", err
	}
	args := append([]string{"commit", "--quiet", "--file=-", "--only", "--"}, paths...)
	if _, err := r.git(ctx, []byte(msg.String()), args...); err != nil {
		return "", err
	}
	out, err := r.git(ctx, nil, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Grep searches files known to git (tracked and untracked, honoring .gitignore) for word
// as a whole word and returns matches as "path:line:text", paths relative to r.dir.
func (r *Repo) Grep(ctx context.Context, word string) ([]GrepMatch, error) {
	out, err := r.git(ctx, nil, "grep", "-n", "-w", "-I", "-F", "--untracked", "--full-name", "-z", "-e", word, "--", ".")
	if err != nil {
		var ce *CommandError
		var ee *exec.ExitError
		if errors.As(err, &ce) && errors.As(ce.Err, &ee) && ee.ExitCode() == 1 {
			return nil, nil // no matches
		}
		return nil, err
	}
	var matches []GrepMatch
	var paths []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		// -z separates path and line number with NUL: path\0line\0text
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		matches = append(matches, GrepMatch{Path: parts[0], Line: n, Text: parts[2]})
		paths = append(paths, parts[0])
	}
	rel, err := r.relative(ctx, paths)
	if err != nil {
		return nil, err
	}
	for i := range matches {
		matches[i].Path = rel[i]
	}
	return matches, nil
}

// GrepMatch is one line found by Grep.
type GrepMatch struct {
	Path string
	Line int
	Text string
}

// CommitInfo is a foreman commit found in the history.
type CommitInfo struct {
	Hash     string
	Time     time.Time
	Subject  string
	Trailers map[string]string
}

// ForemanCommits returns commits that touch the project directory and carry the
// Foreman-Written-By trailer, newest first. A zero since means the whole history.
func (r *Repo) ForemanCommits(ctx context.Context, since time.Time) ([]CommitInfo, error) {
	args := []string{"log", "--format=%H%x1f%aI%x1f%s%x1f%(trailers:only,unfold)%x1e", "--grep=^" + TrailerWrittenBy + ":"}
	if !since.IsZero() {
		args = append(args, "--since="+since.Format(time.RFC3339))
	}
	out, err := r.git(ctx, nil, append(args, "--", ".")...)
	if err != nil {
		// A repository without commits has no history to report.
		if _, herr := r.git(ctx, nil, "rev-parse", "--verify", "-q", "HEAD"); herr != nil {
			return nil, nil
		}
		return nil, err
	}
	var commits []CommitInfo
	for _, rec := range strings.Split(out, "\x1e") {
		f := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x1f", 4)
		if len(f) < 4 {
			continue
		}
		c := CommitInfo{Hash: f[0], Subject: f[2], Trailers: map[string]string{}}
		c.Time, _ = time.Parse(time.RFC3339, f[1])
		for _, line := range strings.Split(f[3], "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.HasPrefix(k, "Foreman-") {
				c.Trailers[k] = strings.TrimSpace(v)
			}
		}
		if c.Trailers[TrailerWrittenBy] != "" {
			commits = append(commits, c)
		}
	}
	return commits, nil
}
