// Package budget fits prompt parts into a generator slot's context window, dropping the
// least important parts first.
package budget

import (
	"context"
	"errors"
	"fmt"
)

// Priority orders parts; a higher value is dropped earlier.
type Priority int

// Priorities from the plan: task and current code are never dropped; then, in order of
// what survives longest: referenced symbols, style guide, file outline, file imports.
const (
	Required Priority = iota + 1
	ContextSymbols
	Style
	Outline
	Imports
)

// Part is one block of prompt text.
type Part struct {
	Key      string
	Priority Priority
	Text     string
	tokens   int
}

// Counter counts tokens of a text.
type Counter func(ctx context.Context, text string) (int, error)

// ErrContextTooLarge means the required parts alone exceed the limit.
var ErrContextTooLarge = errors.New("context_too_large")

// Result is the outcome of Fit.
type Result struct {
	Kept    []Part
	Dropped []string // keys of dropped parts
	Tokens  int
}

// Fit keeps parts (in their original order) whose total fits in limit tokens. Parts are
// dropped from the highest priority value down, the last part of a priority first.
func Fit(ctx context.Context, parts []Part, limit int, count Counter) (Result, error) {
	total := 0
	for i := range parts {
		n, err := count(ctx, parts[i].Text)
		if err != nil {
			return Result{}, fmt.Errorf("count tokens: %w", err)
		}
		parts[i].tokens = n
		total += n
	}
	keep := make([]bool, len(parts))
	for i := range keep {
		keep[i] = true
	}
	var dropped []string
	for total > limit {
		victim := -1
		for i := len(parts) - 1; i >= 0; i-- {
			if keep[i] && parts[i].Priority != Required && (victim < 0 || parts[i].Priority > parts[victim].Priority) {
				victim = i
			}
		}
		if victim < 0 {
			return Result{Tokens: total}, fmt.Errorf("%w: the task, interface and current code need %d tokens, the slot allows %d", ErrContextTooLarge, total, limit)
		}
		keep[victim] = false
		total -= parts[victim].tokens
		dropped = append(dropped, parts[victim].Key)
	}
	res := Result{Dropped: dropped, Tokens: total}
	for i, p := range parts {
		if keep[i] {
			res.Kept = append(res.Kept, p)
		}
	}
	return res, nil
}
