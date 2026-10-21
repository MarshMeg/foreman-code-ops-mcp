// Package style reads the project style guide (.foreman/style.md) and selects the parts
// relevant to one language, to save generator context.
package style

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
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
