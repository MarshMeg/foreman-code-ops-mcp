package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Answer is a scripted generator answer.
type Answer struct {
	Code    string   `json:"code"`
	Imports []string `json:"imports"`
}

// FakeLLM is an OpenAI-compatible chat server with llama.cpp's /props and /tokenize.
type FakeLLM struct {
	URL string // base URL including /v1

	mu      sync.Mutex
	answers []string
	prompts []string // all message contents of each request, joined
	// OnRequest runs before answering request n (0-based).
	OnRequest func(n int)
	// Slots and SlotCtx are reported by /props.
	Slots, SlotCtx int
	// Respond, if set, answers instead of the queue (prompt is all message contents joined).
	Respond func(prompt string) Answer
}

// NewFakeLLM starts a server answering with the given answers in order.
func NewFakeLLM(t *testing.T, answers ...Answer) *FakeLLM {
	t.Helper()
	f := &FakeLLM{Slots: 3, SlotCtx: 65536}
	for _, a := range answers {
		f.AddRaw(mustJSON(a))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		var parts []string
		for _, m := range body.Messages {
			parts = append(parts, m.Content)
		}
		f.mu.Lock()
		n := len(f.prompts)
		f.prompts = append(f.prompts, strings.Join(parts, "\n---\n"))
		hook := f.OnRequest
		f.mu.Unlock()
		if hook != nil {
			hook(n)
		}
		f.mu.Lock()
		if respond := f.Respond; respond != nil {
			f.mu.Unlock()
			writeAnswer(w, mustJSON(respond(strings.Join(parts, "\n---\n"))))
			return
		}
		if len(f.answers) == 0 {
			f.mu.Unlock()
			t.Errorf("fake LLM: unexpected request %d", n)
			http.Error(w, "no more answers", http.StatusInternalServerError)
			return
		}
		a := f.answers[0]
		f.answers = f.answers[1:]
		f.mu.Unlock()
		writeAnswer(w, a)
	})
	mux.HandleFunc("GET /props", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"total_slots": f.Slots, "default_generation_settings": map[string]any{"n_ctx": f.SlotCtx}})
	})
	mux.HandleFunc("POST /tokenize", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Content string }
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]any{"tokens": make([]int, (len(body.Content)+3)/4)})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.URL = srv.URL + "/v1"
	return f
}

// AddRaw queues a raw answer text.
func (f *FakeLLM) AddRaw(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, text)
}

// Prompts returns the message contents of each request so far.
func (f *FakeLLM) Prompts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.prompts...)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func writeAnswer(w http.ResponseWriter, content string) {
	json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 20},
	})
}
