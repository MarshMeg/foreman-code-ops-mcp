package ops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/gitops"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/style"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/workspace"
)

// StyleInfo is the project style guide.
type StyleInfo struct {
	Exists   bool
	Content  string
	Sections []string
}

// StyleGet reads .foreman/style.md.
func (o *Ops) StyleGet() (StyleInfo, error) {
	md, err := style.Read(o.proj.Root)
	if err != nil {
		return StyleInfo{}, err
	}
	return StyleInfo{Exists: md != "", Content: md, Sections: style.Sections(md)}, nil
}

// StyleSet replaces the style guide, or one "## section" of it, and commits.
func (o *Ops) StyleSet(ctx context.Context, section, content string) (Result, error) {
	if strings.TrimSpace(content) == "" {
		return Result{}, errors.New("content is empty")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	md, err := style.Read(o.proj.Root)
	if err != nil {
		return Result{}, err
	}
	next := strings.TrimSpace(content) + "\n"
	subject := "style: update style guide"
	if section != "" {
		next = style.SetSection(md, section, content)
		subject = "style: update section " + strings.TrimSpace(strings.TrimPrefix(section, "## "))
	}
	rel := filepath.ToSlash(filepath.Join(config.DirName, "style.md"))
	cs := workspace.New(o.proj.Root)
	if err := cs.Write(rel, []byte(next)); err != nil {
		return Result{}, err
	}
	return o.commit(ctx, cs, gitops.Message{Subject: subject})
}
