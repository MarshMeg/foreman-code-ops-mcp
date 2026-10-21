// Package config defines foreman-code-ops configuration and loads it from
// layered sources: defaults < global file < project file < environment < CLI.
package config

import (
	"errors"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the full server configuration.
type Config struct {
	Generator Generator `yaml:"generator" json:"generator"`
	Validator Validator `yaml:"validator" json:"validator"`
	Limits    Limits    `yaml:"limits" json:"limits"`
	Git       Git       `yaml:"git" json:"git"`
	Graph     Graph     `yaml:"graph" json:"graph"`
	Log       Log       `yaml:"log" json:"log"`
}

// Generator configures the model that writes code.
type Generator struct {
	Provider      string  `yaml:"provider" json:"provider"`
	BaseURL       string  `yaml:"base_url" json:"base_url,omitempty"`
	Model         string  `yaml:"model" json:"model"`
	APIKeyEnv     string  `yaml:"api_key_env" json:"api_key_env,omitempty"`
	Temperature   float64 `yaml:"temperature" json:"temperature"`
	Slots         int     `yaml:"slots" json:"slots"`
	SlotCtx       int     `yaml:"slot_ctx" json:"slot_ctx"`
	OutputReserve int     `yaml:"output_reserve" json:"output_reserve"`
	Structured    string  `yaml:"structured" json:"structured"`
}

// Validator configures the model that reviews code and writes fallbacks.
type Validator struct {
	Provider  string   `yaml:"provider" json:"provider"`
	BaseURL   string   `yaml:"base_url" json:"base_url,omitempty"`
	Model     string   `yaml:"model" json:"model,omitempty"`
	APIKeyEnv string   `yaml:"api_key_env" json:"api_key_env,omitempty"`
	Timeout   Duration `yaml:"timeout" json:"timeout"`
	Batch     Batch    `yaml:"batch" json:"batch"`
}

// Batch is reserved for asynchronous bulk review (not used in the main path).
type Batch struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// Limits bounds the generate/fix/review loop.
type Limits struct {
	MaxBuildFixes       int      `yaml:"max_build_fixes" json:"max_build_fixes"`
	MaxReviews          int      `yaml:"max_reviews" json:"max_reviews"`
	MaxFallbackAttempts int      `yaml:"max_fallback_attempts" json:"max_fallback_attempts"`
	MaxParallelReviews  int      `yaml:"max_parallel_reviews" json:"max_parallel_reviews"`
	GenerateTimeout     Duration `yaml:"generate_timeout" json:"generate_timeout"`
	CheckTimeout        Duration `yaml:"check_timeout" json:"check_timeout"`
	TestTimeout         Duration `yaml:"test_timeout" json:"test_timeout"`
}

// LimitsOverride is a per-call override of Limits; nil fields keep the configured value.
type LimitsOverride struct {
	MaxBuildFixes       *int      `json:"max_build_fixes,omitempty" jsonschema:"build/lint fix attempts by the generator"`
	MaxReviews          *int      `json:"max_reviews,omitempty" jsonschema:"validator review rounds"`
	MaxFallbackAttempts *int      `json:"max_fallback_attempts,omitempty" jsonschema:"gate attempts for validator-written code"`
	GenerateTimeout     *Duration `json:"generate_timeout,omitempty" jsonschema:"generation timeout, e.g. 300s"`
	CheckTimeout        *Duration `json:"check_timeout,omitempty" jsonschema:"gate timeout, e.g. 120s"`
	TestTimeout         *Duration `json:"test_timeout,omitempty" jsonschema:"test timeout, e.g. 600s"`
}

// With returns l with the non-nil fields of o applied.
func (l Limits) With(o *LimitsOverride) Limits {
	if o == nil {
		return l
	}
	if o.MaxBuildFixes != nil {
		l.MaxBuildFixes = *o.MaxBuildFixes
	}
	if o.MaxReviews != nil {
		l.MaxReviews = *o.MaxReviews
	}
	if o.MaxFallbackAttempts != nil {
		l.MaxFallbackAttempts = *o.MaxFallbackAttempts
	}
	if o.GenerateTimeout != nil {
		l.GenerateTimeout = *o.GenerateTimeout
	}
	if o.CheckTimeout != nil {
		l.CheckTimeout = *o.CheckTimeout
	}
	if o.TestTimeout != nil {
		l.TestTimeout = *o.TestTimeout
	}
	return l
}

// Git configures how operations are committed.
type Git struct {
	// Dirty is "refuse" or "include": what to do when touched files have uncommitted user changes.
	Dirty string `yaml:"dirty" json:"dirty"`
	// Branch, if set, is a branch to commit to instead of the current one.
	Branch string `yaml:"branch" json:"branch,omitempty"`
}

// Graph configures the external code graph provider.
type Graph struct {
	// Provider is codebase-memory, graphify, command or noop.
	Provider string `yaml:"provider" json:"provider"`
	// Command is the provider's executable (default: codebase-memory-mcp / graphify).
	Command string `yaml:"command" json:"command,omitempty"`
	// Project is the codebase-memory project name; empty looks it up by the project path.
	Project string `yaml:"project" json:"project,omitempty"`
	// AutoIndex is true when the graph keeps itself fresh (codebase-memory watcher or
	// auto_index, graphify git hook), so foreman does not reindex after commits.
	AutoIndex bool `yaml:"auto_index" json:"auto_index"`
	// Reindex and References are argv templates for provider "command". Placeholders:
	// {root} {files} {file} {symbol} {name} {project}. References prints JSON or
	// "path:line: text" lines.
	Reindex    []string `yaml:"reindex" json:"reindex,omitempty"`
	References []string `yaml:"references" json:"references,omitempty"`
}

// Log configures diagnostic logging (always to stderr; stdout carries MCP).
type Log struct {
	Level string `yaml:"level" json:"level"`
	File  string `yaml:"file" json:"file,omitempty"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Generator: Generator{
			Provider:      "openai",
			BaseURL:       "http://127.0.0.1:8080/v1",
			Model:         "local",
			Temperature:   0.2,
			Slots:         3,
			SlotCtx:       65536,
			OutputReserve: 8192,
			Structured:    "json_schema",
		},
		Validator: Validator{
			Provider: "openrouter",
			Model:    "z-ai/glm-5.3-flash",
			Timeout:  Duration(120 * time.Second),
		},
		Limits: Limits{
			MaxBuildFixes:       3,
			MaxReviews:          2,
			MaxFallbackAttempts: 2,
			MaxParallelReviews:  8,
			GenerateTimeout:     Duration(300 * time.Second),
			CheckTimeout:        Duration(120 * time.Second),
			TestTimeout:         Duration(600 * time.Second),
		},
		Git:   Git{Dirty: "refuse"},
		Graph: Graph{Provider: "codebase-memory"},
		Log:   Log{Level: "info"},
	}
}

var (
	generatorProviders = []string{"openai", "openrouter", "anthropic", "agent-sdk"}
	validatorProviders = []string{"openai", "openrouter", "anthropic", "agent-sdk", "none"}
	structuredModes    = []string{"json_schema", "grammar", "none"}
	dirtyModes         = []string{"refuse", "include"}
	graphProviders     = []string{"codebase-memory", "graphify", "command", "noop"}
	logLevels          = []string{"debug", "info", "warn", "error"}
)

// Validate reports all configuration errors at once.
func (c Config) Validate() error {
	var errs []error
	check := func(cond bool, format string, args ...any) {
		if !cond {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}
	oneOf := func(field, v string, allowed []string) {
		for _, a := range allowed {
			if v == a {
				return
			}
		}
		errs = append(errs, fmt.Errorf("%s: %q is not one of %v", field, v, allowed))
	}

	oneOf("generator.provider", c.Generator.Provider, generatorProviders)
	oneOf("generator.structured", c.Generator.Structured, structuredModes)
	check(c.Generator.Model != "", "generator.model is required")
	check(c.Generator.Slots > 0, "generator.slots must be > 0, got %d", c.Generator.Slots)
	check(c.Generator.OutputReserve > 0, "generator.output_reserve must be > 0, got %d", c.Generator.OutputReserve)
	check(c.Generator.SlotCtx > c.Generator.OutputReserve,
		"generator.slot_ctx (%d) must exceed generator.output_reserve (%d)", c.Generator.SlotCtx, c.Generator.OutputReserve)

	oneOf("validator.provider", c.Validator.Provider, validatorProviders)
	if c.Validator.Provider != "none" {
		check(c.Validator.Model != "", "validator.model is required unless validator.provider is none")
	}

	check(c.Limits.MaxBuildFixes >= 0, "limits.max_build_fixes must be >= 0")
	check(c.Limits.MaxReviews >= 0, "limits.max_reviews must be >= 0")
	check(c.Limits.MaxFallbackAttempts >= 0, "limits.max_fallback_attempts must be >= 0")
	check(c.Limits.MaxParallelReviews > 0, "limits.max_parallel_reviews must be > 0")

	oneOf("git.dirty", c.Git.Dirty, dirtyModes)
	oneOf("graph.provider", c.Graph.Provider, graphProviders)
	if c.Graph.Provider == "command" {
		check(len(c.Graph.References) > 0, "graph.references is required for graph.provider command")
	}
	oneOf("log.level", c.Log.Level, logLevels)
	return errors.Join(errs...)
}

// Duration is a time.Duration written as a Go duration string ("120s") in YAML and JSON.
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	return d.parse(s)
}

func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

func (d *Duration) UnmarshalText(b []byte) error { return d.parse(string(b)) }

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Duration) parse(s string) error {
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if v < 0 {
		return fmt.Errorf("invalid duration %q: must not be negative", s)
	}
	*d = Duration(v)
	return nil
}
