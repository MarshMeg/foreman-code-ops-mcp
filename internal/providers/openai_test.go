package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeLlama is a scripted OpenAI-compatible server with llama.cpp's native endpoints.
type fakeLlama struct {
	mu       sync.Mutex
	answers  []string
	requests []map[string]any
	// rejectResponseFormat makes the server return 400 for response_format requests.
	rejectResponseFormat bool
}

func (f *fakeLlama) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, body)
		if _, ok := body["response_format"]; ok && f.rejectResponseFormat {
			http.Error(w, `{"error":{"message":"response_format not supported"}}`, http.StatusBadRequest)
			return
		}
		if len(f.answers) == 0 {
			t.Errorf("unexpected request %v", body)
			http.Error(w, "no more answers", 500)
			return
		}
		a := f.answers[0]
		f.answers = f.answers[1:]
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": a}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
	})
	mux.HandleFunc("GET /props", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"total_slots":2,"default_generation_settings":{"n_ctx":32768}}`))
	})
	mux.HandleFunc("POST /tokenize", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Content string }
		json.NewDecoder(r.Body).Decode(&body)
		tokens := make([]int, len(strings.Fields(body.Content)))
		json.NewEncoder(w).Encode(map[string]any{"tokens": tokens})
	})
	return mux
}

func newFake(t *testing.T, f *fakeLlama, mode string) *OpenAI {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return NewOpenAI(OpenAIOptions{BaseURL: srv.URL + "/v1", Model: "m", APIKey: "k", Structured: mode})
}

func genRequest() Request {
	return Request{System: "sys", Messages: []Message{{Role: "user", Content: "write"}}, Schema: GeneratorSchema(), SchemaName: "code"}
}

func TestStructuredOK(t *testing.T) {
	f := &fakeLlama{answers: []string{"```json\n{\"code\": \"func F() {}\", \"imports\": []}\n```"}}
	c := newFake(t, f, StructuredJSONSchema)
	var out GeneratedCode
	msgs, usage, err := Structured(context.Background(), c, genRequest(), &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.Code != "func F() {}" || usage.PromptTokens != 10 || len(msgs) != 2 || msgs[1].Role != "assistant" {
		t.Fatalf("out=%+v usage=%+v msgs=%+v", out, usage, msgs)
	}
	req := f.requests[0]
	rf := req["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Fatalf("response_format = %v", rf)
	}
	schema := rf["json_schema"].(map[string]any)["schema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatalf("schema = %v", schema)
	}
	if req["messages"].([]any)[0].(map[string]any)["role"] != "system" {
		t.Fatal("system message missing")
	}
}

func TestStructuredRetriesInvalidAnswer(t *testing.T) {
	f := &fakeLlama{answers: []string{`{"code": 1}`, `{"code": "x", "imports": ["fmt"]}`}}
	c := newFake(t, f, StructuredNone)
	var out GeneratedCode
	msgs, _, err := Structured(context.Background(), c, genRequest(), &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.Code != "x" || len(out.Imports) != 1 {
		t.Fatalf("out = %+v", out)
	}
	if len(msgs) != 4 || !strings.Contains(msgs[2].Content, "rejected") {
		t.Fatalf("msgs = %+v", msgs)
	}
	if _, ok := f.requests[0]["response_format"]; ok {
		t.Fatal("mode none must not send response_format")
	}
}

func TestStructuredGivesUp(t *testing.T) {
	f := &fakeLlama{answers: []string{"no json here", `{"code": "x"}`}}
	c := newFake(t, f, StructuredJSONSchema)
	var out GeneratedCode
	_, _, err := Structured(context.Background(), c, genRequest(), &out)
	if !errors.Is(err, ErrInvalidAnswer) {
		t.Fatalf("err = %v", err)
	}
}

func TestFallbackToGrammar(t *testing.T) {
	f := &fakeLlama{rejectResponseFormat: true, answers: []string{`{"code":"a","imports":[]}`, `{"code":"b","imports":[]}`}}
	c := newFake(t, f, StructuredJSONSchema)
	var out GeneratedCode
	if _, _, err := Structured(context.Background(), c, genRequest(), &out); err != nil {
		t.Fatal(err)
	}
	if c.Mode() != StructuredGrammar {
		t.Fatalf("mode = %s", c.Mode())
	}
	if _, _, err := Structured(context.Background(), c, genRequest(), &out); err != nil || out.Code != "b" {
		t.Fatal(err, out)
	}
	// 1 rejected + 2 grammar requests; grammar requests carry the llama.cpp json_schema field.
	if len(f.requests) != 3 || f.requests[1]["json_schema"] == nil || f.requests[2]["response_format"] != nil {
		t.Fatalf("requests = %v", f.requests)
	}
}

func TestPropsAndTokenize(t *testing.T) {
	c := newFake(t, &fakeLlama{}, StructuredJSONSchema)
	p, err := c.Props(context.Background())
	if err != nil || p.TotalSlots != 2 || p.SlotCtx != 32768 {
		t.Fatalf("props = %+v %v", p, err)
	}
	n, err := c.Tokenizer().CountTokens(context.Background(), "a b c")
	if err != nil || n != 3 {
		t.Fatalf("tokens = %d %v", n, err)
	}
}

func TestExtractJSON(t *testing.T) {
	for in, want := range map[string]string{
		`{"a":1}`:                     `{"a":1}`,
		"```json\n{\"a\":1}\n```":     `{"a":1}`,
		"Here you go: {\"a\":1} done": `{"a":1}`,
	} {
		if got := ExtractJSON(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}
