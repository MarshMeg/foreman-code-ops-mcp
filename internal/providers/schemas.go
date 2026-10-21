package providers

import "github.com/google/jsonschema-go/jsonschema"

// GeneratedCode is the generator's answer.
type GeneratedCode struct {
	Code    string   `json:"code"`
	Imports []string `json:"imports"`
}

// falseSchema rejects everything; as additionalProperties it marshals to false.
func falseSchema() *jsonschema.Schema { return &jsonschema.Schema{Not: &jsonschema.Schema{}} }

// GeneratorSchema is the JSON schema of GeneratedCode. All properties are required and no
// others are allowed, so it is valid for strict structured-output modes.
func GeneratorSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:     "object",
		Required: []string{"code", "imports"},
		Properties: map[string]*jsonschema.Schema{
			"code": {Type: "string", Description: "the complete definition of the symbol"},
			// null is accepted: small models often emit it for "no imports".
			"imports": {
				Types:       []string{"array", "null"},
				Items:       &jsonschema.Schema{Type: "string"},
				Description: "imports the code needs, in the language's syntax; may be empty",
			},
		},
		AdditionalProperties: falseSchema(),
	}
}

// Severity levels of a review verdict.
const (
	SeverityNone  = "none"
	SeverityMinor = "minor"
	SeverityMajor = "major"
)

// Issue is one review finding.
type Issue struct {
	Line    *int    `json:"line"`
	Problem string  `json:"problem"`
	Fix     *string `json:"fix"`
}

// Verdict is the validator's review.
type Verdict struct {
	OK       bool           `json:"ok"`
	Severity string         `json:"severity"`
	Issues   []Issue        `json:"issues"`
	Rewrite  *GeneratedCode `json:"rewrite"`
}

// VerdictSchema is the JSON schema of Verdict, strict-mode compatible (every property
// required, optional values nullable).
func VerdictSchema() *jsonschema.Schema {
	code := GeneratorSchema()
	code.Types = []string{"object", "null"}
	code.Type = ""
	return &jsonschema.Schema{
		Type:     "object",
		Required: []string{"ok", "severity", "issues", "rewrite"},
		Properties: map[string]*jsonschema.Schema{
			"ok":       {Type: "boolean", Description: "true when the code can be committed as is"},
			"severity": {Type: "string", Enum: []any{SeverityNone, SeverityMinor, SeverityMajor}},
			"issues": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type:     "object",
					Required: []string{"line", "problem", "fix"},
					Properties: map[string]*jsonschema.Schema{
						"line":    {Types: []string{"integer", "null"}, Description: "line in the symbol code, 1-based"},
						"problem": {Type: "string"},
						"fix":     {Types: []string{"string", "null"}},
					},
					AdditionalProperties: falseSchema(),
				},
			},
			"rewrite": code,
		},
		AdditionalProperties: falseSchema(),
	}
}
