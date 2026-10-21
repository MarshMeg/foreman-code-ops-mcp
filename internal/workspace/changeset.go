// Package workspace stages file changes in memory, renders their diff and applies them
// atomically: each file is written to a temp file and renamed, and a failure rolls back
// the files already written.
package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

type change struct {
	rel       string
	old       []byte
	oldExists bool
	oldMode   fs.FileMode
	new       []byte
	delete    bool
}

// Changeset is a set of pending file changes under a root directory. Not safe for concurrent use.
type Changeset struct {
	root    string
	changes map[string]*change
}

// New starts an empty changeset rooted at root (absolute).
func New(root string) *Changeset {
	return &Changeset{root: root, changes: map[string]*change{}}
}

// Read returns the current content of rel: the pending content if staged, else the file on disk.
// exists is false for missing files and staged deletions.
func (c *Changeset) Read(rel string) (content []byte, exists bool, err error) {
	if ch, ok := c.changes[rel]; ok {
		return ch.new, !ch.delete, nil
	}
	b, err := os.ReadFile(c.abs(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

// Write stages new content for rel.
func (c *Changeset) Write(rel string, content []byte) error {
	ch, err := c.entry(rel)
	if err != nil {
		return err
	}
	ch.new, ch.delete = content, false
	return nil
}

// Delete stages removal of rel.
func (c *Changeset) Delete(rel string) error {
	ch, err := c.entry(rel)
	if err != nil {
		return err
	}
	ch.new, ch.delete = nil, true
	return nil
}

func (c *Changeset) entry(rel string) (*change, error) {
	if ch, ok := c.changes[rel]; ok {
		return ch, nil
	}
	ch := &change{rel: rel, oldMode: 0o644}
	info, err := os.Stat(c.abs(rel))
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file", rel)
		}
		if ch.old, err = os.ReadFile(c.abs(rel)); err != nil {
			return nil, err
		}
		ch.oldExists, ch.oldMode = true, info.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	c.changes[rel] = ch
	return ch, nil
}

func (c *Changeset) abs(rel string) string { return filepath.Join(c.root, rel) }

func (ch *change) changed() bool {
	if ch.delete {
		return ch.oldExists
	}
	return !ch.oldExists || !bytes.Equal(ch.old, ch.new)
}

// Files lists the paths that actually change, sorted.
func (c *Changeset) Files() []string {
	var out []string
	for rel, ch := range c.changes {
		if ch.changed() {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// Empty reports whether nothing would change.
func (c *Changeset) Empty() bool { return len(c.Files()) == 0 }

// Diff renders a unified diff of all changes with a/ and b/ prefixes.
func (c *Changeset) Diff() string {
	var b strings.Builder
	for _, rel := range c.Files() {
		ch := c.changes[rel]
		from, to := "a/"+rel, "b/"+rel
		if !ch.oldExists {
			from = "/dev/null"
		}
		if ch.delete {
			to = "/dev/null"
		}
		d, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A:        splitLines(ch.old),
			B:        splitLines(ch.new),
			FromFile: from,
			ToFile:   to,
			Context:  3,
		})
		b.WriteString(d)
	}
	return b.String()
}

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	lines := difflib.SplitLines(string(b))
	// SplitLines appends "\n" to the last line; drop the artifact for files ending in a newline.
	if strings.HasSuffix(string(b), "\n") && len(lines) > 0 && lines[len(lines)-1] == "\n" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Apply writes all changes to disk. If any write fails, files already written are restored
// and the error is returned.
func (c *Changeset) Apply() error {
	var done []*change
	for _, rel := range c.Files() {
		ch := c.changes[rel]
		var err error
		if ch.delete {
			err = os.Remove(c.abs(rel))
		} else {
			err = writeAtomic(c.abs(rel), ch.new, ch.oldMode)
		}
		if err != nil {
			if rerr := c.rollback(done); rerr != nil {
				return fmt.Errorf("apply %s: %w (rollback failed: %v)", rel, err, rerr)
			}
			return fmt.Errorf("apply %s: %w", rel, err)
		}
		done = append(done, ch)
	}
	return nil
}

func (c *Changeset) rollback(done []*change) error {
	var errs []error
	for _, ch := range done {
		var err error
		if ch.oldExists {
			err = writeAtomic(c.abs(ch.rel), ch.old, ch.oldMode)
		} else {
			err = os.Remove(c.abs(ch.rel))
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".foreman-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
