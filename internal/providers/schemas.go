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
