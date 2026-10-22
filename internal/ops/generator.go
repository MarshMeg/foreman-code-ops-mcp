package ops

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MarshMeg/foreman-code-ops/internal/budget"
	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/providers"
	"github.com/MarshMeg/foreman-code-ops/internal/scheduler"
)

// generator is the configured code-writing model plus its slots.
type generator struct {
	cfg   config.Generator
	p     providers.Provider // nil when the provider is not available yet
	err   error              // why p is nil
	sched *scheduler.Scheduler

	mu         sync.Mutex
	reconciled bool
	lastTry    time.Time
	slotCtx    int
	tokenizer  providers.Tokenizer
	warnings   []string
}

func newGenerator(cfg config.Config) *generator {
	g := &generator{cfg: cfg.Generator, sched: scheduler.New(cfg.Generator.Slots), slotCtx: cfg.Generator.SlotCtx}
	g.p, g.err = providers.New(providers.Spec{
		Provider:    cfg.Generator.Provider,
		BaseURL:     cfg.Generator.BaseURL,
		Model:       cfg.Generator.Model,
		APIKeyEnv:   cfg.Generator.APIKeyEnv,
		Temperature: cfg.Generator.Temperature,
		Structured:  cfg.Generator.Structured,
		Timeout:     cfg.Limits.GenerateTimeout.D(),

		AgentCommand: strings.Fields(cfg.AgentSDK.Command),
		Effort:       cfg.AgentSDK.Effort,
	})
	if g.err != nil {
		g.err = fmt.Errorf("generator: %w", g.err)
	}
	return g
}

// reconcile compares configured slots with llama-server's /props once it answers, and uses
// the smaller values so slots are never overfilled. Failed attempts are retried at most
// every 30 seconds.
func (g *generator) reconcile(ctx context.Context) {
	o, ok := g.p.(*providers.OpenAI)
	if !ok {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reconciled || time.Since(g.lastTry) < 30*time.Second {
		return
	}
	g.lastTry = time.Now()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	props, err := o.Props(ctx)
	if err != nil {
		g.warnings = []string{fmt.Sprintf("generator: llama-server /props unavailable (%v); using configured slots and estimated token counts", err)}
		return
	}
	g.reconciled = true
	g.warnings = nil
	g.tokenizer = o.Tokenizer()
	slots := g.cfg.Slots
	if props.TotalSlots > 0 && props.TotalSlots != g.cfg.Slots {
		g.warnings = append(g.warnings, fmt.Sprintf("generator: config has %d slots, llama-server has %d (-np); using %d", g.cfg.Slots, props.TotalSlots, min(slots, props.TotalSlots)))
		slots = min(slots, props.TotalSlots)
	}
	if props.SlotCtx > 0 && props.SlotCtx != g.cfg.SlotCtx {
		g.warnings = append(g.warnings, fmt.Sprintf("generator: config slot_ctx is %d, llama-server slot context is %d (-c / -np); using %d", g.cfg.SlotCtx, props.SlotCtx, min(g.cfg.SlotCtx, props.SlotCtx)))
		g.slotCtx = min(g.cfg.SlotCtx, props.SlotCtx)
	}
	g.sched.SetSlots(slots)
}

// counter counts tokens with llama-server when available, else estimates.
func (g *generator) counter() budget.Counter {
	g.mu.Lock()
	tok := g.tokenizer
	g.mu.Unlock()
	return func(ctx context.Context, text string) (int, error) {
		if tok != nil {
			if n, err := tok.CountTokens(ctx, text); err == nil {
				return n, nil
			}
		}
		return providers.EstimateTokens(text), nil
	}
}

func (g *generator) effectiveSlotCtx() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.slotCtx
}

func (g *generator) status() (warnings []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := append([]string{}, g.warnings...)
	if g.err != nil {
		out = append(out, g.err.Error())
	}
	return out
}

// validator is the reviewing and fallback-writing model.
type validator struct {
	p     providers.Provider // nil when disabled or unavailable
	err   error              // why a configured validator is unavailable
	sched *scheduler.Scheduler
}

func newValidator(cfg config.Config) *validator {
	v := &validator{sched: scheduler.New(cfg.Limits.MaxParallelReviews)}
	v.p, v.err = providers.New(providers.Spec{
		Provider:   cfg.Validator.Provider,
		BaseURL:    cfg.Validator.BaseURL,
		Model:      cfg.Validator.Model,
		APIKeyEnv:  cfg.Validator.APIKeyEnv,
		Structured: providers.StructuredJSONSchema,
		Timeout:    cfg.Validator.Timeout.D(),

		AgentCommand: strings.Fields(cfg.AgentSDK.Command),
		Effort:       cfg.AgentSDK.Effort,
	})
	if v.err != nil {
		v.err = fmt.Errorf("validator unavailable, code is not reviewed: %w", v.err)
	}
	return v
}
