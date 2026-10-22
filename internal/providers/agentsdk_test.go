package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// TestMain lets the test binary act as the sidecar (FOREMAN_FAKE_SIDECAR=<mode>).
func TestMain(m *testing.M) {
	if mode := os.Getenv("FOREMAN_FAKE_SIDECAR"); mode != "" {
		fakeSidecar(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeSidecar answers each request with a JSON object describing it. Modes:
// echo; crash-once (exits with a message on the first request if the marker file is absent);
// hang (never answers, but acknowledges cancel requests).
func fakeSidecar(mode string) {
	var mu sync.Mutex
	send := func(v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		os.Stdout.Write(append(b, '\n'))
		mu.Unlock()
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var req sidecarRequest
		json.Unmarshal(sc.Bytes(), &req)
		switch {
		case req.Cancel:
			send(map[string]any{"id": req.ID, "error": "aborted"})
		case mode == "crash-once" && !exists(os.Getenv("FOREMAN_FAKE_MARKER")):
			os.WriteFile(os.Getenv("FOREMAN_FAKE_MARKER"), nil, 0o644)
			fmt.Fprintln(os.Stderr, "Error: Native CLI binary for linux-x64 not found")
			os.Exit(3)
		case mode == "hang":
		default:
			go func(req sidecarRequest) {
				// Answer out of order to exercise id matching.
				time.Sleep(time.Duration(10-req.ID%10) * time.Millisecond)
				answer, _ := json.Marshal(map[string]any{
					"model": req.Model, "effort": req.Effort, "system": req.System,
					"turns": len(req.Messages), "schema": req.Schema != nil, "last": req.Messages[len(req.Messages)-1].Content,
				})
				send(map[string]any{"id": req.ID, "text": string(answer),
					"usage": map[string]any{"prompt_tokens": 120, "completion_tokens": 7, "cached_tokens": 100, "cost": 0.01}})
			}(req)
		}
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func fakeAgent(t *testing.T, mode string, extraEnv ...string) *AgentSDK {
	t.Helper()
	t.Setenv("FOREMAN_FAKE_SIDECAR", mode)
	for i := 0; i+1 < len(extraEnv); i += 2 {
		t.Setenv(extraEnv[i], extraEnv[i+1])
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewAgentSDK(AgentSDKOptions{Command: []string{exe, "-test.run=^$"}, Model: "claude-opus-5-5", Effort: "low", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

type echoed struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
	System string `json:"system"`
	Turns  int    `json:"turns"`
	Schema bool   `json:"schema"`
	Last   string `json:"last"`
}

func TestAgentSDKStructured(t *testing.T) {
	a := fakeAgent(t, "echo")
	schema, err := jsonschema.For[echoed](nil)
	if err != nil {
		t.Fatal(err)
	}
	var out echoed
	_, usage, err := Structured(context.Background(), a, Request{System: "sys", Messages: []Message{{Role: "user", Content: "write f"}}, Schema: schema}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out != (echoed{Model: "claude-opus-5-5", Effort: "low", System: "sys", Turns: 1, Schema: true, Last: "write f"}) {
		t.Fatalf("sidecar saw %+v", out)
	}
	if usage != (Usage{PromptTokens: 120, CompletionTokens: 7, CachedTokens: 100, Cost: 0.01}) || a.Name() != "agent-sdk/claude-opus-5-5" {
		t.Fatalf("usage %+v, name %s", usage, a.Name())
	}
}

func TestAgentSDKConcurrent(t *testing.T) {
	a := fakeAgent(t, "echo")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg := fmt.Sprintf("req %d", i)
			resp, err := a.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: msg}}})
			if err != nil {
				t.Error(err)
				return
			}
			var e echoed
			if json.Unmarshal([]byte(resp.Text), &e); e.Last != msg {
				t.Errorf("request %q got answer for %q", msg, e.Last)
			}
		}()
	}
	wg.Wait()
}

func TestAgentSDKRestartsAfterCrash(t *testing.T) {
	a := fakeAgent(t, "crash-once", "FOREMAN_FAKE_MARKER", t.TempDir()+"/crashed")
	req := Request{Messages: []Message{{Role: "user", Content: "x"}}}
	_, err := a.Complete(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "sidecar exited") || !strings.Contains(err.Error(), "Native CLI binary") {
		t.Fatalf("first request: %v", err)
	}
	// The next request starts a new process.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err = a.Complete(context.Background(), req)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("after restart: %v", err)
	}
}

func TestAgentSDKCancel(t *testing.T) {
	a := fakeAgent(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := a.Complete(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	a.mu.Lock()
	n := len(a.pending)
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d requests still pending", n)
	}
}

func TestAgentSDKMissingCommand(t *testing.T) {
	a, err := New(Spec{Provider: "agent-sdk", Model: "m", AgentCommand: []string{"/nonexistent/foreman-agent-sdk"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "pnpm add -g foreman-agent-sdk-") {
		t.Fatalf("got %v", err)
	}
	if _, err := New(Spec{Provider: "agent-sdk", Model: "m"}); err == nil {
		t.Fatal("empty command accepted")
	}
}
