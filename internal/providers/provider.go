// Package providers talks to the models that write and review code. Every provider returns
// text; Structured turns it into a schema-validated JSON value with one corrective retry.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"` // user | assistant
	Content string `json:"content"`
}

// Request is a completion request.
type Request struct {
	System   string
	Messages []Message
	// Schema constrains the answer to JSON when the provider supports it.
	Schema     *jsonschema.Schema
	SchemaName string
	MaxTokens  int
}

// Usage reports token counts when the provider returns them.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	// CachedTokens is the part of PromptTokens served from the provider's prompt cache.
	CachedTokens int `json:"cached_tokens"`
	// Cost in USD when the provider reports it (OpenRouter).
	Cost float64 `json:"cost,omitempty"`
}

// Add accumulates u into t.
func (t *Usage) Add(u Usage) {
	t.PromptTokens += u.PromptTokens
	t.CompletionTokens += u.CompletionTokens
	t.CachedTokens += u.CachedTokens
	t.Cost += u.Cost
}

// Response is a completion.
type Response struct {
	Text  string
	Usage Usage
}

// Provider is a chat completion backend.
type Provider interface {
	// Name identifies provider and model for logs and commit trailers, e.g. "openai/qwen".
	Name() string
	// Model is the model name alone.
	Model() string
	Complete(ctx context.Context, req Request) (Response, error)
}

// Tokenizer counts tokens exactly; providers without one are estimated.
type Tokenizer interface {
	CountTokens(ctx context.Context, text string) (int, error)
}

// EstimateTokens is a rough count for providers without a tokenizer (about 3.5 bytes per token for code).
func EstimateTokens(text string) int {
	return (len(text)*2 + 6) / 7
}

// ErrInvalidAnswer means the model's answer did not match the schema even after a retry.
var ErrInvalidAnswer = errors.New("model answer does not match the schema")

// Structured completes req and decodes the JSON answer into out, validating it against
// req.Schema. On an invalid answer it retries once, telling the model what was wrong.
// The returned transcript ends with the accepted assistant answer, for follow-up turns.
func Structured(ctx context.Context, p Provider, req Request, out any) ([]Message, Usage, error) {
	if req.Schema == nil {
		return nil, Usage{}, errors.New("structured request needs a schema")
	}
	resolved, err := req.Schema.Resolve(nil)
	if err != nil {
		return nil, Usage{}, fmt.Errorf("schema: %w", err)
	}
	var total Usage
	msgs := append([]Message{}, req.Messages...)
	for attempt := 0; attempt < 2; attempt++ {
		r := req
		r.Messages = msgs
		resp, err := p.Complete(ctx, r)
		if err != nil {
			return nil, total, err
		}
		total.Add(resp.Usage)
		msgs = append(msgs, Message{Role: "assistant", Content: resp.Text})

		verr := decode(resp.Text, resolved, out)
		if verr == nil {
			return msgs, total, nil
		}
		if attempt == 1 {
			return msgs, total, fmt.Errorf("%w: %v", ErrInvalidAnswer, verr)
		}
		msgs = append(msgs, Message{Role: "user", Content: "Your answer was rejected: " + verr.Error() +
			"\nReply again with only a JSON object that matches the required schema. No prose, no Markdown."})
	}
	panic("unreachable")
}

func decode(text string, schema *jsonschema.Resolved, out any) error {
	raw := ExtractJSON(text)
	var instance any
	if err := json.Unmarshal([]byte(raw), &instance); err != nil {
		return fmt.Errorf("not valid JSON: %v", err)
	}
	if err := schema.Validate(instance); err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), out)
}

// ExtractJSON strips Markdown fences and leading/trailing prose around a JSON object.
func ExtractJSON(text string) string {
	s := strings.TrimSpace(text)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	if start, end := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}'); start > 0 && end > start {
		s = s[start : end+1]
	}
	return s
}
