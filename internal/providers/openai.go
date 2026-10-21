package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Structured output modes for OpenAI-compatible servers.
const (
	// StructuredJSONSchema sends response_format {type: json_schema} (OpenAI, vLLM, llama-server).
	StructuredJSONSchema = "json_schema"
	// StructuredGrammar sends llama.cpp's top-level json_schema field, which the server compiles
	// to a GBNF grammar. Used as the fallback when response_format is rejected.
	StructuredGrammar = "grammar"
	// StructuredNone relies on instructions and server-side validation only.
	StructuredNone = "none"
)

// OpenAI is a client for OpenAI-compatible chat completion APIs (llama-server, vLLM,
// LM Studio, OpenRouter, OpenAI).
type OpenAI struct {
	name        string
	baseURL     string // ends without a slash, includes /v1
	model       string
	apiKey      string
	temperature float64
	headers     map[string]string
	http        *http.Client
	// mode is the structured output mode; it may degrade from json_schema to grammar at runtime.
	mode atomic.Value
}

// OpenAIOptions configures an OpenAI client.
type OpenAIOptions struct {
	Name        string // provider label, e.g. "openai" or "openrouter"
	BaseURL     string
	Model       string
	APIKey      string
	Temperature float64
	Structured  string
	Headers     map[string]string
	Timeout     time.Duration
}

// NewOpenAI creates a client.
func NewOpenAI(o OpenAIOptions) *OpenAI {
	c := &OpenAI{
		name:        o.Name,
		baseURL:     strings.TrimRight(o.BaseURL, "/"),
		model:       o.Model,
		apiKey:      o.APIKey,
		temperature: o.Temperature,
		headers:     o.Headers,
		http:        &http.Client{Timeout: o.Timeout},
	}
	if c.name == "" {
		c.name = "openai"
	}
	mode := o.Structured
	if mode == "" {
		mode = StructuredJSONSchema
	}
	c.mode.Store(mode)
	return c
}

func (c *OpenAI) Name() string  { return c.name + "/" + c.model }
func (c *OpenAI) Model() string { return c.model }

// Mode returns the current structured output mode.
func (c *OpenAI) Mode() string { return c.mode.Load().(string) }

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat any             `json:"response_format,omitempty"`
	JSONSchema     json.RawMessage `json:"json_schema,omitempty"` // llama.cpp extension
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// HTTPError is a non-2xx response.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, strings.TrimSpace(e.Body))
}

// Complete implements Provider.
func (c *OpenAI) Complete(ctx context.Context, req Request) (Response, error) {
	resp, err := c.complete(ctx, req, c.Mode())
	var he *HTTPError
	if err != nil && req.Schema != nil && c.Mode() == StructuredJSONSchema && errors.As(err, &he) && he.Status == http.StatusBadRequest {
		// The server does not accept response_format with a schema; fall back for good.
		c.mode.Store(StructuredGrammar)
		return c.complete(ctx, req, StructuredGrammar)
	}
	return resp, err
}

func (c *OpenAI) complete(ctx context.Context, req Request, mode string) (Response, error) {
	msgs := make([]Message, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, Message{Role: "system", Content: req.System})
	}
	msgs = append(msgs, req.Messages...)
	body := chatRequest{Model: c.model, Messages: msgs, Temperature: c.temperature, MaxTokens: req.MaxTokens}
	if req.Schema != nil {
		schema, err := json.Marshal(req.Schema)
		if err != nil {
			return Response{}, err
		}
		switch mode {
		case StructuredJSONSchema:
			name := req.SchemaName
			if name == "" {
				name = "answer"
			}
			body.ResponseFormat = map[string]any{
				"type":        "json_schema",
				"json_schema": map[string]any{"name": name, "schema": json.RawMessage(schema), "strict": true},
			}
		case StructuredGrammar:
			body.JSONSchema = schema
		}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}
	var out chatResponse
	if err := c.post(ctx, c.baseURL+"/chat/completions", data, &out); err != nil {
		return Response{}, err
	}
	if out.Error != nil {
		return Response{}, fmt.Errorf("%s: %s", c.Name(), out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return Response{}, fmt.Errorf("%s: empty response", c.Name())
	}
	ch := out.Choices[0]
	if ch.FinishReason == "length" {
		return Response{}, fmt.Errorf("%s: answer truncated at max_tokens (%d)", c.Name(), req.MaxTokens)
	}
	return Response{Text: ch.Message.Content, Usage: out.Usage}, nil
}

func (c *OpenAI) post(ctx context.Context, url string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", c.Name(), err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		if len(b) > 2000 {
			b = b[:2000]
		}
		return &HTTPError{Status: resp.StatusCode, Body: string(b)}
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%s: decode response: %w", c.Name(), err)
	}
	return nil
}
