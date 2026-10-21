// Package journal records every tool operation and pipeline attempt as JSON lines,
// so metrics can be computed later (see `foreman-code-ops stats`).
package journal

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one journal record.
type Entry struct {
	Time       time.Time      `json:"time"`
	Tool       string         `json:"tool"`
	File       string         `json:"file,omitempty"`
	Symbol     string         `json:"symbol,omitempty"`
	Status     string         `json:"status"`
	WrittenBy  string         `json:"written_by,omitempty"`
	BuildFixes int            `json:"build_fixes,omitempty"`
	Reviews    int            `json:"reviews,omitempty"`
	Commit     string         `json:"commit,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	Error      string         `json:"error,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

// Journal appends entries to a writer. It is safe for concurrent use.
type Journal struct {
	mu    sync.Mutex
	w     io.Writer
	close func() error
	now   func() time.Time
}

// Open appends to the JSONL file at path, creating parent directories.
func Open(path string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Journal{w: f, close: f.Close, now: time.Now}, nil
}

// New writes to w; Close is a no-op.
func New(w io.Writer) *Journal {
	return &Journal{w: w, close: func() error { return nil }, now: time.Now}
}

// Discard returns a journal that drops all entries.
func Discard() *Journal { return New(io.Discard) }

// Record writes e, filling Time if unset.
func (j *Journal) Record(e Entry) error {
	if e.Time.IsZero() {
		e.Time = j.now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	b = append(b, '\n')
	j.mu.Lock()
	defer j.mu.Unlock()
	_, err = j.w.Write(b)
	return err
}

// Close closes the underlying file.
func (j *Journal) Close() error { return j.close() }
