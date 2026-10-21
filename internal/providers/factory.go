package providers

import (
	"fmt"
	"os"
	"time"
)

// Spec selects and configures a provider.
type Spec struct {
	Provider    string // openai | openrouter | anthropic | agent-sdk | none
	BaseURL     string // empty: the provider's default
	Model       string
	APIKeyEnv   string
	Temperature float64
	Structured  string
	Timeout     time.Duration
}

// DefaultBaseURL returns the endpoint used when Spec.BaseURL is empty.
func DefaultBaseURL(provider string) string {
	switch provider {
	case "openai":
		return "http://127.0.0.1:8080/v1"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	}
	return ""
}

// DefaultAPIKeyEnv returns the environment variable read when Spec.APIKeyEnv is empty.
func DefaultAPIKeyEnv(provider string) string {
	switch provider {
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	}
	return ""
}

// New builds a provider. It returns (nil, nil) for "none".
func New(s Spec) (Provider, error) {
	if s.APIKeyEnv == "" {
		s.APIKeyEnv = DefaultAPIKeyEnv(s.Provider)
	}
	key := ""
	if s.APIKeyEnv != "" {
		key = os.Getenv(s.APIKeyEnv)
	}
	base := s.BaseURL
	if base == "" {
		base = DefaultBaseURL(s.Provider)
	}
	switch s.Provider {
	case "none", "":
		return nil, nil
	case "openai":
		return NewOpenAI(OpenAIOptions{Name: "openai", BaseURL: base, Model: s.Model, APIKey: key,
			Temperature: s.Temperature, Structured: s.Structured, Timeout: s.Timeout}), nil
	case "openrouter":
		if key == "" {
			return nil, fmt.Errorf("openrouter: API key env %q is not set", s.APIKeyEnv)
		}
		return NewOpenAI(OpenAIOptions{Name: "openrouter", BaseURL: base, Model: s.Model, APIKey: key,
			Temperature: s.Temperature, Structured: s.Structured, Timeout: s.Timeout,
			// OpenRouter has no grammar mode: models without response_format get instructions only.
			FallbackMode: StructuredNone,
			ReportCost:   true,
			Headers: map[string]string{
				"HTTP-Referer": "https://github.com/MarshMeg/foreman-code-ops",
				"X-Title":      "foreman-code-ops",
			}}), nil
	case "anthropic":
		// With no key the SDK resolves other credentials (ANTHROPIC_AUTH_TOKEN, `ant auth login` profiles).
		return NewAnthropic(AnthropicOptions{APIKey: key, BaseURL: s.BaseURL, Model: s.Model, Timeout: s.Timeout}), nil
	case "agent-sdk":
		return nil, fmt.Errorf("provider agent-sdk is not implemented yet")
	default:
		return nil, fmt.Errorf("unknown provider %q", s.Provider)
	}
}
