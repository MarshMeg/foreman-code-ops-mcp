package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// LlamaProps is the part of llama-server's /props response foreman uses.
type LlamaProps struct {
	TotalSlots int `json:"total_slots"`
	// SlotCtx is the context size of one slot (default_generation_settings.n_ctx).
	SlotCtx int
}

// serverRoot strips a trailing /v1 so llama.cpp's native endpoints can be reached.
func (c *OpenAI) serverRoot() string {
	return strings.TrimSuffix(c.baseURL, "/v1")
}

// Props queries llama-server's /props. Other servers return an error, which callers treat
// as "not a llama-server".
func (c *OpenAI) Props(ctx context.Context) (LlamaProps, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverRoot()+"/props", nil)
	if err != nil {
		return LlamaProps{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return LlamaProps{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return LlamaProps{}, fmt.Errorf("/props: HTTP %d", resp.StatusCode)
	}
	var raw struct {
		TotalSlots int `json:"total_slots"`
		Defaults   struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return LlamaProps{}, err
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return LlamaProps{}, fmt.Errorf("/props: %w", err)
	}
	if raw.TotalSlots == 0 && raw.Defaults.NCtx == 0 {
		return LlamaProps{}, fmt.Errorf("/props: not a llama-server response")
	}
	return LlamaProps{TotalSlots: raw.TotalSlots, SlotCtx: raw.Defaults.NCtx}, nil
}

// LlamaTokenizer counts tokens with llama-server's /tokenize.
type LlamaTokenizer struct{ c *OpenAI }

// Tokenizer returns a tokenizer backed by llama-server's /tokenize endpoint.
func (c *OpenAI) Tokenizer() *LlamaTokenizer { return &LlamaTokenizer{c: c} }

// CountTokens implements Tokenizer.
func (t *LlamaTokenizer) CountTokens(ctx context.Context, text string) (int, error) {
	body, _ := json.Marshal(map[string]any{"content": text, "add_special": false})
	var out struct {
		Tokens []json.RawMessage `json:"tokens"`
	}
	if err := t.c.post(ctx, t.c.serverRoot()+"/tokenize", body, &out); err != nil {
		return 0, err
	}
	return len(out.Tokens), nil
}
