// Package logging builds the server logger. Logs never go to stdout, which carries MCP traffic.
package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// New returns a JSON logger writing to stderr and, if file is set, also to that file.
// The returned close function releases the file.
func New(level, file string) (*slog.Logger, func() error, error) {
	var w io.Writer = os.Stderr
	closeFn := func() error { return nil }
	if file != "" {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return nil, nil, err
		}
		f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, err
		}
		w = io.MultiWriter(os.Stderr, f)
		closeFn = f.Close
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: parseLevel(level)})
	return slog.New(h), closeFn, nil
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
