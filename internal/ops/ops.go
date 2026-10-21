// Package ops implements the file and symbol operations behind the MCP tools.
// Every change goes through a workspace.Changeset and ends in a git commit.
package ops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/edit"
	"github.com/MarshMeg/foreman-code-ops/internal/gate"
	"github.com/MarshMeg/foreman-code-ops/internal/gitops"
	"github.com/MarshMeg/foreman-code-ops/internal/lang"
	"github.com/MarshMeg/foreman-code-ops/internal/project"
	"github.com/MarshMeg/foreman-code-ops/internal/workspace"
)

// Result statuses.
const (
	StatusApplied   = "applied"
	StatusUnchanged = "unchanged"
	StatusRefused   = "refused"
	StatusFailed    = "failed"
)

// maxReferences caps the references reported by delete_symbol.
const maxReferences = 50

// Result describes the outcome of a change.
type Result struct {
	Status      string
	Message     string
	Diagnostics []gate.Diagnostic
	Diff        string
	Commit      string
	References  []string
	Warnings    []string
}

// Ops serializes changes to one project.
type Ops struct {
	proj *project.Project
	reg  *lang.Registry
	gate *gate.Gate
	cfg  config.Config
	log  *slog.Logger

	// mu serializes read-modify-commit sequences so commits never interleave.
	mu   sync.Mutex
	repo *gitops.Repo
}

// New creates Ops for a project.
func New(proj *project.Project, reg *lang.Registry, cfg config.Config, log *slog.Logger) (*Ops, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	g, err := gate.New(proj, reg, cfg.Limits)
	if err != nil {
		return nil, err
	}
	return &Ops{proj: proj, reg: reg, gate: g, cfg: cfg, log: log}, nil
}

// Project returns the project.
func (o *Ops) Project() *project.Project { return o.proj }

func (o *Ops) gitRepo(ctx context.Context) (*gitops.Repo, error) {
	if o.repo != nil {
		return o.repo, nil
	}
	r, err := gitops.Open(ctx, o.proj.Root)
	if err != nil {
		return nil, err
	}
	o.repo = r
	return r, nil
}

func (o *Ops) adapter(rel string) (lang.Adapter, error) {
	a := o.reg.ForFile(rel)
	if a == nil {
		return nil, fmt.Errorf("no language adapter for %q (extension %q)", rel, filepath.Ext(rel))
	}
	return a, nil
}

func readExisting(abs, rel string) ([]byte, error) {
	b, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("file %q does not exist", rel)
	}
	return b, err
}

// SymbolInfo is a symbol with its source text.
type SymbolInfo struct {
	File   string
	Symbol lang.Symbol
	Source string
}

// GetSymbol returns a symbol and its source.
func (o *Ops) GetSymbol(file, name string) (SymbolInfo, error) {
	rel, abs, err := o.proj.Resolve(file)
	if err != nil {
		return SymbolInfo{}, err
	}
	a, err := o.adapter(rel)
	if err != nil {
		return SymbolInfo{}, err
	}
	src, err := readExisting(abs, rel)
	if err != nil {
		return SymbolInfo{}, err
	}
	s, err := a.Find(src, name)
	if err != nil {
		return SymbolInfo{}, err
	}
	return SymbolInfo{File: rel, Symbol: s, Source: string(src[s.Start:s.End])}, nil
}

// DeleteSymbol removes a symbol. Without force it refuses when the symbol's name is
// referenced elsewhere (a whole-word name search until a code graph is configured).
func (o *Ops) DeleteSymbol(ctx context.Context, file, name string, force bool) (Result, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	rel, abs, err := o.proj.Resolve(file)
	if err != nil {
		return Result{}, err
	}
	a, err := o.adapter(rel)
	if err != nil {
		return Result{}, err
	}
	src, err := readExisting(abs, rel)
	if err != nil {
		return Result{}, err
	}
	sym, err := a.Find(src, name)
	if err != nil {
		return Result{}, err
	}

	if !force {
		refs, truncated, err := o.references(ctx, rel, sym)
		if err != nil {
			return Result{}, err
		}
		if len(refs) > 0 {
			w := []string{"references found by whole-word name search; some may be unrelated"}
			count := fmt.Sprint(len(refs))
			if truncated {
				w = append(w, fmt.Sprintf("only the first %d references are listed", maxReferences))
				count += "+"
			}
			return Result{
				Status:     StatusRefused,
				Message:    fmt.Sprintf("%s has %s references; update the callers first or pass force=true", name, count),
				References: refs,
				Warnings:   w,
			}, nil
		}
	}

	out, err := edit.Delete(src, sym.Start, sym.End)
	if err != nil {
		return Result{}, err
	}
	cs := workspace.New(o.proj.Root)
	if err := cs.Write(rel, out); err != nil {
		return Result{}, err
	}
	return o.gateAndCommit(ctx, cs, force, gitops.Message{Subject: fmt.Sprintf("delete %s %s", rel, name)})
}

// references finds uses of the symbol's last name segment outside its own definition.
func (o *Ops) references(ctx context.Context, rel string, sym lang.Symbol) ([]string, bool, error) {
	repo, err := o.gitRepo(ctx)
	if err != nil {
		return nil, false, err
	}
	word := sym.Name[strings.LastIndex(sym.Name, ".")+1:]
	matches, err := repo.Grep(ctx, word)
	if err != nil {
		return nil, false, err
	}
	var refs []string
	for _, m := range matches {
		if m.Path == rel && m.Line >= sym.StartLine && m.Line <= sym.EndLine {
			continue
		}
		if len(refs) == maxReferences {
			return refs, true, nil
		}
		text := strings.TrimSpace(m.Text)
		if len(text) > 160 {
			text = text[:160] + "…"
		}
		refs = append(refs, fmt.Sprintf("%s:%d: %s", m.Path, m.Line, text))
	}
	return refs, false, nil
}

// CreateFile creates a new file with the adapter's initial content (e.g. a Go package
// clause inferred from sibling files), or an empty file for unknown extensions.
func (o *Ops) CreateFile(ctx context.Context, file, pkg string) (Result, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	rel, abs, err := o.proj.Resolve(file)
	if err != nil {
		return Result{}, err
	}
	if _, err := os.Lstat(abs); err == nil {
		return Result{}, fmt.Errorf("file %q already exists", rel)
	}
	var content []byte
	if a := o.reg.ForFile(rel); a != nil {
		content, err = a.NewFile(rel, lang.NewFileOpts{Package: pkg, Siblings: siblings(abs)})
		if err != nil {
			return Result{}, err
		}
	}
	cs := workspace.New(o.proj.Root)
	if err := cs.Write(rel, content); err != nil {
		return Result{}, err
	}
	return o.commit(ctx, cs, gitops.Message{Subject: "create " + rel})
}

// siblings reads up to a few files next to abs with the same extension.
func siblings(abs string) [][]byte {
	const limit = 5
	entries, err := os.ReadDir(filepath.Dir(abs))
	if err != nil {
		return nil
	}
	var out [][]byte
	for _, e := range entries {
		if len(out) == limit {
			break
		}
		if e.Type().IsRegular() && path.Ext(e.Name()) == filepath.Ext(abs) && !strings.HasSuffix(e.Name(), "_test.go") {
			if b, err := os.ReadFile(filepath.Join(filepath.Dir(abs), e.Name())); err == nil {
				out = append(out, b)
			}
		}
	}
	return out
}

// DeleteFile removes a file. It refuses when the build breaks without it, unless force.
func (o *Ops) DeleteFile(ctx context.Context, file string, force bool) (Result, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	rel, abs, err := o.proj.Resolve(file)
	if err != nil {
		return Result{}, err
	}
	if _, err := readExisting(abs, rel); err != nil {
		return Result{}, err
	}
	cs := workspace.New(o.proj.Root)
	if err := cs.Delete(rel); err != nil {
		return Result{}, err
	}
	return o.gateAndCommit(ctx, cs, force, gitops.Message{Subject: "delete " + rel})
}

// gateAndCommit fixes imports and formatting in cs, runs the checks, and commits.
// Failing checks refuse the change unless force, in which case it is committed and the
// diagnostics are returned.
func (o *Ops) gateAndCommit(ctx context.Context, cs *workspace.Changeset, force bool, msg gitops.Message) (Result, error) {
	diags, err := o.gate.Prepare(ctx, cs, nil)
	if err != nil {
		return Result{}, err
	}
	if !gate.HasErrors(diags) {
		rep, err := o.gate.Check(ctx, cs, nil)
		if err != nil {
			return Result{}, err
		}
		diags = append(diags, rep.Diagnostics...)
	}
	if gate.HasErrors(diags) && !force {
		return Result{
			Status:      StatusRefused,
			Message:     "the change breaks the build or lint; fix the callers first or pass force=true",
			Diagnostics: diags,
			Diff:        cs.Diff(),
		}, nil
	}
	res, err := o.commit(ctx, cs, msg)
	res.Diagnostics = diags
	return res, err
}

// commit checks for user changes in the touched files, applies cs and commits it.
// The caller holds o.mu.
func (o *Ops) commit(ctx context.Context, cs *workspace.Changeset, msg gitops.Message) (Result, error) {
	files := cs.Files()
	if len(files) == 0 {
		return Result{Status: StatusUnchanged, Message: "nothing to change"}, nil
	}
	repo, err := o.gitRepo(ctx)
	if err != nil {
		return Result{}, err
	}
	dirty, err := repo.Dirty(ctx, files)
	if err != nil {
		return Result{}, err
	}
	var warnings []string
	if len(dirty) > 0 {
		if o.cfg.Git.Dirty != "include" {
			return Result{
				Status:  StatusRefused,
				Message: "uncommitted user changes in " + strings.Join(dirty, ", ") + "; commit or stash them, or set git.dirty: include",
			}, nil
		}
		warnings = append(warnings, "commit includes uncommitted user changes in "+strings.Join(dirty, ", "))
	}
	if o.cfg.Git.Branch != "" {
		if err := repo.EnsureBranch(ctx, o.cfg.Git.Branch); err != nil {
			return Result{}, fmt.Errorf("switch to branch %s: %w", o.cfg.Git.Branch, err)
		}
	}

	diff := cs.Diff()
	if err := cs.Apply(); err != nil {
		return Result{}, err
	}
	msg.Services = o.proj.ServiceNames(files)
	hash, err := repo.Commit(ctx, files, msg)
	if err != nil {
		// The change stays on disk; the caller sees why the commit failed (e.g. a hook).
		o.log.Warn("commit failed", "files", files, "err", err)
		return Result{Status: StatusFailed, Message: "change applied but commit failed: " + err.Error(), Diff: diff, Warnings: warnings}, nil
	}
	o.log.Info("committed", "commit", hash, "files", files, "subject", msg.Subject)
	return Result{Status: StatusApplied, Diff: diff, Commit: hash, Warnings: warnings}, nil
}
