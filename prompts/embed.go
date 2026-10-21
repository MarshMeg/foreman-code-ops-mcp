// Package prompts embeds the LLM prompt templates. They are English Markdown with
// text/template placeholders.
package prompts

import (
	"embed"
	"text/template"
)

//go:embed *.md
var files embed.FS

// Templates holds all prompts by file name, e.g. "generate.md".
var Templates = template.Must(template.New("").Option("missingkey=error").ParseFS(files, "*.md"))
