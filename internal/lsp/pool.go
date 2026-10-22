package lsp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Pool keeps one running server per command and root, so diagnostics come from a warm
// server. Requests to one server are serialized: a document can only hold one candidate.
type Pool struct {
	idle time.Duration

	mu      sync.Mutex
	servers map[string]*entry
	reaping bool
	closed  bool
}

type entry struct {
	mu       sync.Mutex // held for the duration of one Diagnose
	c        *Client
	lastUsed time.Time
	failedAt time.Time
	err      error
}

// retryAfter is how long a server that failed to start is not tried again.
const retryAfter = time.Minute

// NewPool creates a pool; servers unused for idle are shut down.
func NewPool(idle time.Duration) *Pool {
	return &Pool{idle: idle, servers: map[string]*entry{}}
}

// Server describes how to start a language server.
type Server struct {
	Argv        []string
	Dir         string
	Env         []string
	InitOptions map[string]any
}

// Diagnose runs docs through the server for s, starting it when needed.
func (p *Pool) Diagnose(ctx context.Context, s Server, docs []Document, settle time.Duration) ([]Diagnostic, error) {
	key := s.Dir + "\x00" + strings.Join(s.Argv, "\x00")
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("lsp pool is closed")
	}
	e := p.servers[key]
	if e == nil {
		e = &entry{}
		p.servers[key] = e
	}
	if !p.reaping && p.idle > 0 {
		p.reaping = true
		go p.reap()
	}
	p.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.c != nil && !e.c.Alive() {
		e.c = nil
	}
	if e.c == nil {
		if e.err != nil && time.Since(e.failedAt) < retryAfter {
			return nil, e.err
		}
		c, err := Start(ctx, s.Argv, s.Dir, s.Env, s.InitOptions)
		if err != nil {
			e.err, e.failedAt = fmt.Errorf("start %s: %w", s.Argv[0], err), time.Now()
			return nil, e.err
		}
		e.c, e.err = c, nil
	}
	e.lastUsed = time.Now()
	ds, err := e.c.Diagnose(ctx, docs, settle)
	e.lastUsed = time.Now()
	if err != nil && ctx.Err() == nil {
		// A broken server is restarted on the next request.
		e.c.Close()
		e.c = nil
	}
	return ds, err
}

func (p *Pool) reap() {
	t := time.NewTicker(min(p.idle, time.Minute))
	defer t.Stop()
	for range t.C {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		entries := make([]*entry, 0, len(p.servers))
		for _, e := range p.servers {
			entries = append(entries, e)
		}
		p.mu.Unlock()
		for _, e := range entries {
			if e.mu.TryLock() {
				if e.c != nil && time.Since(e.lastUsed) > p.idle {
					e.c.Close()
					e.c = nil
				}
				e.mu.Unlock()
			}
		}
	}
}

// Running reports how many servers are up, for status.
func (p *Pool) Running() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.servers {
		if e.mu.TryLock() {
			if e.c != nil && e.c.Alive() {
				n++
			}
			e.mu.Unlock()
		} else {
			n++ // busy, so running
		}
	}
	return n
}

// Close shuts every server down.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	entries := p.servers
	p.servers = map[string]*entry{}
	p.mu.Unlock()
	for _, e := range entries {
		e.mu.Lock()
		if e.c != nil {
			e.c.Close()
			e.c = nil
		}
		e.mu.Unlock()
	}
}
