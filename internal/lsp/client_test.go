package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain lets the test binary act as a language server (FOREMAN_FAKE_LSP=push|pull|crash).
func TestMain(m *testing.M) {
	if mode := os.Getenv("FOREMAN_FAKE_LSP"); mode != "" {
		fakeServer(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeServer reports an error on every line containing "BAD". In push mode it first asks the
// client for configuration (and waits for the answer), then publishes a preliminary empty
// result followed by the real one, as servers that publish in phases do.
func fakeServer(mode string) {
	r := bufio.NewReader(os.Stdin)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(b), b)
	}
	docs := map[string]string{}
	diagnose := func(uri string) []map[string]any {
		var out []map[string]any
		for i, line := range strings.Split(docs[uri], "\n") {
			if j := strings.Index(line, "BAD"); j >= 0 {
				out = append(out, map[string]any{"range": map[string]any{"start": map[string]any{"line": i, "character": j},
					"end": map[string]any{"line": i, "character": j + 3}}, "severity": 1, "message": "bad token", "code": 42})
			}
		}
		return out
	}
	for {
		body, err := readMessage(r)
		if err != nil {
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		json.Unmarshal(body, &m)
		switch m.Method {
		case "initialize":
			caps := map[string]any{"textDocumentSync": 1}
			if mode == "pull" {
				caps["diagnosticProvider"] = map[string]any{"interFileDependencies": false, "workspaceDiagnostics": false}
			}
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"capabilities": caps}})
		case "textDocument/didOpen":
			var p struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
					Text    string `json:"text"`
				} `json:"textDocument"`
			}
			json.Unmarshal(m.Params, &p)
			if mode == "crash" {
				fmt.Fprintln(os.Stderr, "fatal: out of cheese")
				os.Exit(2)
			}
			docs[p.TextDocument.URI] = p.TextDocument.Text
			if mode == "push" {
				send(map[string]any{"jsonrpc": "2.0", "id": "cfg-1", "method": "workspace/configuration",
					"params": map[string]any{"items": []any{map[string]any{"section": "fake"}}}})
				// Wait for the client's answer before publishing.
				for {
					b, err := readMessage(r)
					if err != nil {
						return
					}
					var a struct {
						ID     string          `json:"id"`
						Result json.RawMessage `json:"result"`
					}
					if json.Unmarshal(b, &a) == nil && a.ID == "cfg-1" {
						break
					}
				}
				uri, v := p.TextDocument.URI, p.TextDocument.Version
				send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
					"params": map[string]any{"uri": uri, "version": v, "diagnostics": []any{}}})
				time.Sleep(50 * time.Millisecond)
				send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
					"params": map[string]any{"uri": uri, "version": v, "diagnostics": diagnose(uri)}})
			}
		case "textDocument/didClose":
		case "textDocument/diagnostic":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			json.Unmarshal(m.Params, &p)
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"kind": "full", "items": diagnose(p.TextDocument.URI)}})
		case "shutdown":
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": nil})
		case "exit":
			return
		}
	}
}

func fakePool(t *testing.T, mode string) (*Pool, Server) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pool := NewPool(time.Minute)
	t.Cleanup(pool.Close)
	return pool, Server{Argv: []string{exe, "-test.run=^$"}, Dir: t.TempDir(), Env: append(os.Environ(), "FOREMAN_FAKE_LSP="+mode)}
}

func TestFakeServerModes(t *testing.T) {
	for _, mode := range []string{"push", "pull"} {
		t.Run(mode, func(t *testing.T) {
			pool, srv := fakePool(t, mode)
			doc := Document{Path: srv.Dir + "/a.x", LanguageID: "x", Text: []byte("ok\n  BAD here\n")}
			ds, err := pool.Diagnose(context.Background(), srv, []Document{doc}, 200*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if len(ds) != 1 || ds[0].Line != 2 || ds[0].Col != 3 || ds[0].Severity != 1 || ds[0].Code != "42" || ds[0].File != doc.Path {
				t.Fatalf("diagnostics %+v", ds)
			}
			// The same document again, now clean: version 2 must not reuse version 1's result.
			doc.Text = []byte("ok\n")
			ds, err = pool.Diagnose(context.Background(), srv, []Document{doc}, 200*time.Millisecond)
			if err != nil || len(ds) != 0 {
				t.Fatalf("clean: %+v %v", ds, err)
			}
			if pool.Running() != 1 {
				t.Fatalf("running = %d", pool.Running())
			}
		})
	}
}

func TestFakeServerCrashAndRetry(t *testing.T) {
	pool, srv := fakePool(t, "crash")
	doc := Document{Path: srv.Dir + "/a.x", LanguageID: "x", Text: []byte("BAD")}
	_, err := pool.Diagnose(context.Background(), srv, []Document{doc}, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "out of cheese") {
		t.Fatalf("got %v", err)
	}
	if pool.Running() != 0 {
		t.Fatal("crashed server still counted")
	}
}

func TestPoolStartFailureIsRemembered(t *testing.T) {
	pool := NewPool(time.Minute)
	defer pool.Close()
	srv := Server{Argv: []string{"/nonexistent/lsp"}, Dir: t.TempDir()}
	var calls atomic.Int32
	for range 3 {
		if _, err := pool.Diagnose(context.Background(), srv, nil, 0); err != nil {
			calls.Add(1)
		}
	}
	if calls.Load() != 3 {
		t.Fatal("expected errors")
	}
	if e := pool.servers[srv.Dir+"\x00/nonexistent/lsp"]; e == nil || e.err == nil {
		t.Fatal("failure not remembered")
	}
}
