package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// AgentSDK talks to the Node sidecar in sidecar/agent-sdk, which runs Claude through the
// Claude Agent SDK (TypeScript and Python only, hence the sidecar). The process starts on the
// first request and stays alive; requests are multiplexed over its stdin/stdout by id.
type AgentSDK struct {
	argv    []string
	model   string
	effort  string
	timeout time.Duration

	mu      sync.Mutex
	proc    *sidecarProc
	nextID  int64
	pending map[int64]chan sidecarResponse
}

// AgentSDKOptions configures the sidecar provider.
type AgentSDKOptions struct {
	// Command starts the sidecar, e.g. ["foreman-agent-sdk"] or ["node", "sidecar/agent-sdk/index.mjs"].
	Command []string
	Model   string
	Effort  string
	Timeout time.Duration
}

type sidecarRequest struct {
	ID       int64          `json:"id"`
	System   string         `json:"system,omitempty"`
	Messages []Message      `json:"messages,omitempty"`
	Schema   map[string]any `json:"schema,omitempty"`
	Model    string         `json:"model,omitempty"`
	Effort   string         `json:"effort,omitempty"`
	Cancel   bool           `json:"cancel,omitempty"`
}

type sidecarResponse struct {
	ID    int64  `json:"id"`
	Text  string `json:"text"`
	Error string `json:"error"`
	Usage *struct {
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		CachedTokens     int     `json:"cached_tokens"`
		Cost             float64 `json:"cost"`
	} `json:"usage"`
}

type sidecarProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *tailBuffer
	done   chan struct{}
}

// NewAgentSDK creates the provider; the sidecar is not started until the first request.
func NewAgentSDK(o AgentSDKOptions) (*AgentSDK, error) {
	if len(o.Command) == 0 {
		return nil, errors.New("agent-sdk: agent_sdk.command is empty")
	}
	return &AgentSDK{argv: o.Command, model: o.Model, effort: o.Effort, timeout: o.Timeout,
		pending: map[int64]chan sidecarResponse{}}, nil
}

func (a *AgentSDK) Name() string  { return "agent-sdk/" + a.model }
func (a *AgentSDK) Model() string { return a.model }

// Complete implements Provider.
func (a *AgentSDK) Complete(ctx context.Context, req Request) (Response, error) {
	if a.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.timeout)
		defer cancel()
	}
	sr := sidecarRequest{System: req.System, Messages: req.Messages, Model: a.model, Effort: a.effort}
	if req.Schema != nil {
		raw, err := json.Marshal(req.Schema)
		if err != nil {
			return Response{}, err
		}
		if err := json.Unmarshal(raw, &sr.Schema); err != nil {
			return Response{}, err
		}
	}

	a.mu.Lock()
	if err := a.startLocked(); err != nil {
		a.mu.Unlock()
		return Response{}, err
	}
	a.nextID++
	sr.ID = a.nextID
	ch := make(chan sidecarResponse, 1)
	a.pending[sr.ID] = ch
	proc := a.proc
	err := writeLine(proc.stdin, sr)
	a.mu.Unlock()
	if err != nil {
		a.forget(sr.ID)
		return Response{}, fmt.Errorf("%s: write to sidecar: %w", a.Name(), err)
	}

	select {
	case resp := <-ch:
		if resp.Error != "" {
			return Response{}, fmt.Errorf("%s: %s", a.Name(), resp.Error)
		}
		out := Response{Text: resp.Text}
		if u := resp.Usage; u != nil {
			out.Usage = Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, CachedTokens: u.CachedTokens, Cost: u.Cost}
		}
		return out, nil
	case <-ctx.Done():
		a.forget(sr.ID)
		a.mu.Lock()
		if a.proc == proc {
			_ = writeLine(proc.stdin, sidecarRequest{ID: sr.ID, Cancel: true})
		}
		a.mu.Unlock()
		return Response{}, fmt.Errorf("%s: %w", a.Name(), ctx.Err())
	}
}

func (a *AgentSDK) forget(id int64) {
	a.mu.Lock()
	delete(a.pending, id)
	a.mu.Unlock()
}

// startLocked starts the sidecar if it is not running. The caller holds a.mu.
func (a *AgentSDK) startLocked() error {
	if a.proc != nil {
		return nil
	}
	cmd := exec.Command(a.argv[0], a.argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	p := &sidecarProc{cmd: cmd, stdin: stdin, stderr: &tailBuffer{max: 4096}, done: make(chan struct{})}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: start sidecar %q: %w (install it with: npm install -g ./sidecar/agent-sdk, or set agent_sdk.command)",
			a.Name(), strings.Join(a.argv, " "), err)
	}
	a.proc = p
	go a.read(p, stdout)
	return nil
}

// read delivers responses until the sidecar exits, then fails what is still pending; the next
// request starts a new process.
func (a *AgentSDK) read(p *sidecarProc, stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var resp sidecarResponse
		if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
			continue
		}
		a.mu.Lock()
		ch := a.pending[resp.ID]
		delete(a.pending, resp.ID)
		a.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
	err := p.cmd.Wait()
	close(p.done)
	msg := "sidecar exited"
	if err != nil {
		msg += ": " + err.Error()
	}
	if tail := strings.TrimSpace(p.stderr.String()); tail != "" {
		msg += "\n" + tail
	}
	a.mu.Lock()
	if a.proc == p {
		a.proc = nil
	}
	for id, ch := range a.pending {
		ch <- sidecarResponse{ID: id, Error: msg}
		delete(a.pending, id)
	}
	a.mu.Unlock()
}

// Close stops the sidecar: stdin is closed so it finishes running requests, and it is killed
// if it does not exit in time.
func (a *AgentSDK) Close() error {
	a.mu.Lock()
	p := a.proc
	a.mu.Unlock()
	if p == nil {
		return nil
	}
	p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
	return nil
}

func writeLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}
