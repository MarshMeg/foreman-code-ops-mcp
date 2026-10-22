package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

// fallbackModels accept server-side refusal fallbacks in their "default" form, which
// routes a declined request to another model by refusal category.
var fallbackModels = []string{"claude-fable-5-1", "claude-opus-5-5", "claude-opus-5"}

// Anthropic calls the Claude Messages API through the official SDK. Structured answers use
// structured outputs (output_config.format); the system prompt is cached.
type Anthropic struct {
	client anthropic.Client
	model  string
}

// AnthropicOptions configures an Anthropic client.
type AnthropicOptions struct {
	APIKey  string
	BaseURL string // optional
	Model   string
	Timeout time.Duration
}

// NewAnthropic creates a client.
func NewAnthropic(o AnthropicOptions) *Anthropic {
	var opts []option.RequestOption
	if o.APIKey != "" {
		opts = append(opts, option.WithAPIKey(o.APIKey))
	}
	if o.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(o.BaseURL))
	}
	if o.Timeout > 0 {
		opts = append(opts, option.WithRequestTimeout(o.Timeout))
	}
	return &Anthropic{client: anthropic.NewClient(opts...), model: o.Model}
}

func (a *Anthropic) Name() string  { return "anthropic/" + a.model }
func (a *Anthropic) Model() string { return a.model }

// Complete implements Provider.
func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 16000
	}
	params := anthropic.BetaMessageNewParams{
		Model:     a.model,
		MaxTokens: maxTokens,
	}
	if req.System != "" {
		// The system prompt is identical across calls of one kind (review, write), so cache it.
		params.System = []anthropic.BetaTextBlockParam{{Text: req.System, CacheControl: anthropic.NewBetaCacheControlEphemeralParam()}}
	}
	for i, m := range req.Messages {
		block := anthropic.BetaTextBlockParam{Text: m.Content}
		// Cache the first user turn: it carries the stable context (style, symbols).
		if i == 0 && len(req.Messages) > 1 {
			block.CacheControl = anthropic.NewBetaCacheControlEphemeralParam()
		}
		role := anthropic.BetaMessageParamRoleUser
		if m.Role == "assistant" {
			role = anthropic.BetaMessageParamRoleAssistant
		}
		params.Messages = append(params.Messages, anthropic.BetaMessageParam{
			Role:    role,
			Content: []anthropic.BetaContentBlockParamUnion{{OfText: &block}},
		})
	}
	if req.Schema != nil {
		raw, err := json.Marshal(req.Schema)
		if err != nil {
			return Response{}, err
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			return Response{}, err
		}
		params.OutputConfig = anthropic.BetaOutputConfigParam{Format: anthropic.BetaJSONOutputFormatParam{Schema: schema}}
	}
	if slices.Contains(fallbackModels, a.model) {
		params.Betas = append(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
		params.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
	}

	resp, err := a.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return Response{}, fmt.Errorf("%s: %w", a.Name(), err)
	}
	switch resp.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return Response{}, fmt.Errorf("%s: the model declined the request (%s)", a.Name(), resp.StopDetails.Category)
	case anthropic.BetaStopReasonMaxTokens:
		return Response{}, fmt.Errorf("%s: answer truncated at max_tokens (%d)", a.Name(), maxTokens)
	}
	var text strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	return Response{
		Text: text.String(),
		Usage: Usage{
			PromptTokens:     int(resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens + resp.Usage.CacheCreationInputTokens),
			CompletionTokens: int(resp.Usage.OutputTokens),
			CachedTokens:     int(resp.Usage.CacheReadInputTokens),
		},
	}, nil
}
