// Package adapters embeds the built-in language adapters (adapter.yaml + queries + prompts).
package adapters

import "embed"

// FS holds one directory per language.
//
//go:embed */*.yaml */*.scm
var FS embed.FS
