// Package style reads the project style guide (.foreman/style.md) and selects the parts
// relevant to one language, to save generator context.
package style

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
)

// Path returns <root>/.foreman/style.md.
func Path(root string) string { return filepath.Join(root, config.DirName, "style.md") }

// Read returns the style guide, or "" if there is none.
func Read(root string) (string, error) {
	b, err := os.ReadFile(Path(root))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// aliases maps heading names to adapter languages.
var aliases = map[string]string{
	"go": "go", "golang": "go",
	"python":     "python",
	"typescript": "typescript", "ts": "typescript",
	"javascript": "javascript", "js": "javascript",
	"tsx": "tsx", "jsx": "tsx", "react": "tsx",
}

// Select keeps the common part of the guide and the "## <Language>" sections for lang,
// dropping sections for other languages.
func Select(md, lang string) string {
	var out []string
	keep := true
	for _, line := range strings.SplitAfter(md, "\n") {
		if strings.HasPrefix(line, "## ") {
			heading := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			l, isLang := aliases[heading]
			keep = !isLang || l == lang || (lang == "tsx" && l == "typescript")
		}
		if keep {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, ""))
}

// SetSection replaces the "## <section>" block of md (heading matched case-insensitively)
// with content, or appends it when the section does not exist. content may include its
// own heading; one is added otherwise.
func SetSection(md, section, content string) string {
	section = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(section), "## "))
	body := strings.TrimSpace(content)
	lines := strings.SplitAfter(md, "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimPrefix(line, "## "))
		if start < 0 && strings.EqualFold(heading, section) {
			start = i
			section = heading // keep the existing spelling
		} else if start >= 0 {
			end = i
			break
		}
	}
	if !strings.HasPrefix(body, "## ") {
		body = "## " + section + "\n\n" + body
	}
	if start < 0 {
		md = strings.TrimRight(md, "\n")
		if md == "" {
			return body + "\n"
		}
		return md + "\n\n" + body + "\n"
	}
	before := strings.Join(lines[:start], "")
	after := strings.Join(lines[end:], "")
	out := before + body + "\n"
	if after != "" {
		out += "\n" + after
	}
	return out
}

// Sections lists the "## " headings of md.
func Sections(md string) []string {
	var out []string
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "## ") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, "## ")))
		}
	}
	return out
}
