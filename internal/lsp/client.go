// Package lsp is a minimal Language Server Protocol client for diagnostics: it opens candidate
// file contents in a running server (gopls, pyright, typescript-language-server) and collects
// the errors the server reports, without writing anything to disk.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Diagnostic is one problem reported by the server, with 1-based positions.
type Diagnostic struct {
	File     string // absolute path
	Line     int
	Col      int
	Severity int // 1 error, 2 warning, 3 information, 4 hint
	Message  string
	Source   string
	Code     string
}

// Document is a file to diagnose: its absolute path, language id and content.
type Document struct {
	Path       string
	LanguageID string
	Text       []byte
}

// Client is one language server process.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *tail
	root   string

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	calls   map[int64]chan response
	// published holds the latest pushed diagnostics per URI and wakes waiters.
	published map[string]published
	notify    chan struct{}
	done      chan struct{}
	exitErr   error

	pull     bool // the server answers textDocument/diagnostic
	versions map[string]int
}

type published struct {
	version int // 0 when the server does not send one
	seq     int
	diags   []wireDiagnostic
}

type response struct {
	Result json.RawMessage
	Error  *rpcError
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s (code %d)", e.Message, e.Code) }

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type wireDiagnostic struct {
	Range struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
	} `json:"range"`
	Severity int             `json:"severity"`
	Code     json.RawMessage `json:"code"`
	Source   string          `json:"source"`
	Message  string          `json:"message"`
}

// Start launches the server with argv in dir (the project or service root) and initializes it.
func Start(ctx context.Context, argv []string, dir string, env []string, initOptions map[string]any) (*Client, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd, stdin: stdin, stderr: &tail{max: 4096}, root: dir,
		calls: map[int64]chan response{}, published: map[string]published{}, notify: make(chan struct{}),
		done: make(chan struct{}), versions: map[string]int{}}
	cmd.Stderr = c.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go c.read(stdout)

	rootURI := FileURI(dir)
	params := map[string]any{
		"processId":        nil,
		"rootUri":          rootURI,
		"workspaceFolders": []map[string]any{{"uri": rootURI, "name": filepath.Base(dir)}},
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization":    map[string]any{"didSave": false},
				"publishDiagnostics": map[string]any{"versionSupport": true},
				"diagnostic":         map[string]any{"dynamicRegistration": false},
			},
			"workspace": map[string]any{"configuration": true, "workspaceFolders": true},
			"window":    map[string]any{"workDoneProgress": false},
		},
	}
	if initOptions != nil {
		params["initializationOptions"] = initOptions
	}
	var init struct {
		Capabilities struct {
			DiagnosticProvider json.RawMessage `json:"diagnosticProvider"`
		} `json:"capabilities"`
	}
	if err := c.call(ctx, "initialize", params, &init); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	c.pull = len(init.Capabilities.DiagnosticProvider) > 0 && string(init.Capabilities.DiagnosticProvider) != "null"
	if err := c.notifyServer("initialized", map[string]any{}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Diagnose opens the documents with their content, collects diagnostics for them and closes
// them again, so the server falls back to the files on disk. It is not safe to call
// concurrently for the same documents; callers serialize per client.
func (c *Client) Diagnose(ctx context.Context, docs []Document, settle time.Duration) ([]Diagnostic, error) {
	type opened struct {
		uri     string
		version int
		seq     int
	}
	var open []opened
	defer func() {
		for _, o := range open {
			_ = c.notifyServer("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": o.uri}})
		}
	}()
	for _, d := range docs {
		uri := FileURI(d.Path)
		c.mu.Lock()
		c.versions[uri]++
		v := c.versions[uri]
		seq := c.published[uri].seq
		c.mu.Unlock()
		err := c.notifyServer("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": d.LanguageID, "version": v, "text": string(d.Text)}})
		if err != nil {
			return nil, err
		}
		open = append(open, opened{uri, v, seq})
	}

	var out []Diagnostic
	for i, o := range open {
		var diags []wireDiagnostic
		var err error
		if c.pull {
			diags, err = c.pullDiagnostics(ctx, o.uri)
		} else {
			diags, err = c.waitPublished(ctx, o.uri, o.version, o.seq, settle)
		}
		if err != nil {
			return nil, err
		}
		for _, w := range diags {
			out = append(out, Diagnostic{File: docs[i].Path, Line: w.Range.Start.Line + 1, Col: w.Range.Start.Character + 1,
				Severity: severityOr(w.Severity), Message: w.Message, Source: w.Source, Code: codeString(w.Code)})
		}
	}
	return out, nil
}

func (c *Client) pullDiagnostics(ctx context.Context, uri string) ([]wireDiagnostic, error) {
	var report struct {
		Kind  string           `json:"kind"`
		Items []wireDiagnostic `json:"items"`
	}
	if err := c.call(ctx, "textDocument/diagnostic", map[string]any{"textDocument": map[string]any{"uri": uri}}, &report); err != nil {
		return nil, fmt.Errorf("textDocument/diagnostic: %w", err)
	}
	return report.Items, nil
}

// waitPublished waits for diagnostics pushed for uri after it was opened (for version when the
// server reports versions), then for settle without further updates, since servers may first
// publish syntax errors and later the semantic ones.
func (c *Client) waitPublished(ctx context.Context, uri string, version, seq int, settle time.Duration) ([]wireDiagnostic, error) {
	var last published
	got := false
	timer := (<-chan time.Time)(nil)
	for {
		c.mu.Lock()
		p, ch := c.published[uri], c.notify
		c.mu.Unlock()
		if p.seq > seq && (p.version == 0 || p.version >= version) && (!got || p.seq != last.seq) {
			last, got = p, true
			timer = time.After(settle)
		}
		select {
		case <-ch:
		case <-timer:
			return last.diags, nil
		case <-c.done:
			return nil, c.exitError()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Alive reports whether the server process is still running.
func (c *Client) Alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

// Close shuts the server down politely, then kills it.
func (c *Client) Close() error {
	if !c.Alive() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.call(ctx, "shutdown", nil, nil)
	_ = c.notifyServer("exit", nil)
	c.stdin.Close()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
	return nil
}

func (c *Client) exitError() error {
	msg := "language server exited"
	if c.exitErr != nil {
		msg += ": " + c.exitErr.Error()
	}
	if t := strings.TrimSpace(c.stderr.String()); t != "" {
		msg += "\n" + t
	}
	return errors.New(msg)
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan response, 1)
	c.calls[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.calls, id)
		c.mu.Unlock()
	}()
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case r := <-ch:
		if r.Error != nil {
			return r.Error
		}
		if out != nil && len(r.Result) > 0 {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	case <-c.done:
		return c.exitError()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) notifyServer(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.send(msg)
}

func (c *Client) send(msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	_, err = c.stdin.Write(b)
	return err
}

func (c *Client) read(stdout io.Reader) {
	r := bufio.NewReader(stdout)
	for {
		body, err := readMessage(r)
		if err != nil {
			break
		}
		var m wireMessage
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			c.answerServer(m)
		case m.Method == "textDocument/publishDiagnostics":
			var p struct {
				URI         string           `json:"uri"`
				Version     int              `json:"version"`
				Diagnostics []wireDiagnostic `json:"diagnostics"`
			}
			if json.Unmarshal(m.Params, &p) == nil {
				c.mu.Lock()
				prev := c.published[p.URI]
				c.published[p.URI] = published{version: p.Version, seq: prev.seq + 1, diags: p.Diagnostics}
				close(c.notify)
				c.notify = make(chan struct{})
				c.mu.Unlock()
			}
		case m.Method == "":
			id, err := strconv.ParseInt(string(m.ID), 10, 64)
			if err != nil {
				continue
			}
			c.mu.Lock()
			ch := c.calls[id]
			c.mu.Unlock()
			if ch != nil {
				ch <- response{Result: m.Result, Error: m.Error}
			}
		}
	}
	c.exitErr = c.cmd.Wait()
	close(c.done)
}

// answerServer replies to requests from the server. Servers block on some of them
// (workspace/configuration), so each gets a neutral answer.
func (c *Client) answerServer(m wireMessage) {
	var result any
	switch m.Method {
	case "workspace/configuration":
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(m.Params, &p)
		result = make([]any, len(p.Items))
	case "workspace/workspaceFolders":
		result = []map[string]any{{"uri": FileURI(c.root), "name": filepath.Base(c.root)}}
	}
	_ = c.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			if length, err = strconv.Atoi(strings.TrimSpace(v)); err != nil {
				return nil, err
			}
		}
	}
	if length < 0 {
		return nil, errors.New("lsp: message without Content-Length")
	}
	body := make([]byte, length)
	_, err := io.ReadFull(r, body)
	return body, err
}

// FileURI turns an absolute path into a file:// URI.
func FileURI(p string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(p)}).String()
}

func severityOr(s int) int {
	if s == 0 {
		return 1 // the spec leaves it to the client; treat as an error
	}
	return s
}

func codeString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

type tail struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}
