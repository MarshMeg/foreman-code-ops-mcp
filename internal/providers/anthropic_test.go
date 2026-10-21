package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeAnthropic(t *testing.T, stopReason, text string, got *map[string]any) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("X-Api-Key") != "k" {
			t.Errorf("api key header = %q", r.Header.Get("X-Api-Key"))
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		*got = map[string]any{"body": body, "beta": r.Header.Get("Anthropic-Beta")}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": "m",
			"content":      []any{map[string]any{"type": "text", "text": text}},
			"stop_reason":  stopReason,
			"stop_details": map[string]any{"type": "refusal", "category": "cyber", "explanation": "x"},
			"usage":        map[string]any{"input_tokens": 10, "output_tokens": 5, "cache_read_input_tokens": 90},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestAnthropicStructured(t *testing.T) {
	var got map[string]any
	url := fakeAnthropic(t, "end_turn", `{"code":"func F() {}","imports":[]}`, &got)
	a := NewAnthropic(AnthropicOptions{APIKey: "k", BaseURL: url, Model: "claude-opus-5-5"})
	var out GeneratedCode
	_, usage, err := Structured(context.Background(), a, genRequest(), &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.Code != "func F() {}" || usage.CachedTokens != 90 || usage.PromptTokens != 100 {
		t.Fatalf("out=%+v usage=%+v", out, usage)
	}
	body := got["body"].(map[string]any)
	format := body["output_config"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"].(map[string]any)["additionalProperties"] != false {
		t.Errorf("output_config = %v", body["output_config"])
	}
	sys := body["system"].([]any)[0].(map[string]any)
	if sys["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Errorf("system = %v", sys)
	}
	if body["fallbacks"] != "default" || !strings.Contains(got["beta"].(string), "server-side-fallback-2026-07-01") {
		t.Errorf("fallbacks = %v beta = %v", body["fallbacks"], got["beta"])
	}
	if _, ok := body["tool_choice"]; ok {
		t.Error("forced tool_choice must not be sent")
	}
}

func TestAnthropicNoFallbackForOtherModels(t *testing.T) {
	var got map[string]any
	url := fakeAnthropic(t, "end_turn", `{"code":"x","imports":null}`, &got)
	a := NewAnthropic(AnthropicOptions{APIKey: "k", BaseURL: url, Model: "claude-haiku-5-5"})
	var out GeneratedCode
	if _, _, err := Structured(context.Background(), a, genRequest(), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["body"].(map[string]any)["fallbacks"]; ok {
		t.Error("fallbacks sent for a model that does not accept them")
	}
}

func TestAnthropicRefusal(t *testing.T) {
	var got map[string]any
	url := fakeAnthropic(t, "refusal", "", &got)
	a := NewAnthropic(AnthropicOptions{APIKey: "k", BaseURL: url, Model: "claude-opus-5-5"})
	_, err := a.Complete(context.Background(), genRequest())
	if err == nil || !strings.Contains(err.Error(), "declined") || !strings.Contains(err.Error(), "cyber") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerdictSchemaValidates(t *testing.T) {
	resolved, err := VerdictSchema().Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var v Verdict
	ok := `{"ok":false,"severity":"major","issues":[{"line":null,"problem":"p","fix":"f"}],"rewrite":{"code":"c","imports":[]}}`
	if err := decode(ok, resolved, &v); err != nil || v.Rewrite == nil || v.Issues[0].Fix == nil {
		t.Fatalf("valid verdict rejected: %v %+v", err, v)
	}
	for _, bad := range []string{
		`{"ok":true,"severity":"huge","issues":[],"rewrite":null}`,
		`{"ok":true,"severity":"none","issues":[]}`,
		`{"ok":true,"severity":"none","issues":[{"problem":"p"}],"rewrite":null}`,
	} {
		if err := decode(bad, resolved, &v); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
