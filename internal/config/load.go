package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// DirName is the per-project directory.
	DirName = ".foreman"
	// EnvPrefix prefixes all environment overrides.
	EnvPrefix = "FOREMAN_"
)

// ProjectConfigPath returns <project>/.foreman/config.yaml.
func ProjectConfigPath(projectDir string) string {
	return filepath.Join(projectDir, DirName, "config.yaml")
}

// GlobalConfigPath returns ~/.config/foreman-code-ops/config.yaml (respecting XDG_CONFIG_HOME).
func GlobalConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "foreman-code-ops", "config.yaml"), nil
}

// Options describes where configuration comes from.
type Options struct {
	// GlobalPath is the global config file; empty skips it.
	GlobalPath string
	// ProjectDir is the project root; empty skips the project file.
	ProjectDir string
	// LookupEnv reads environment variables; nil skips the environment.
	LookupEnv func(string) (string, bool)
	// Flags holds CLI overrides keyed by setting key (see Keys); only explicitly set flags belong here.
	Flags map[string]string
}

// Load merges defaults, global file, project file, environment and CLI flags (later wins)
// and validates the result.
func Load(opts Options) (Config, error) {
	cfg := Default()
	if opts.GlobalPath != "" {
		if err := mergeFile(&cfg, opts.GlobalPath); err != nil {
			return cfg, err
		}
	}
	if opts.ProjectDir != "" {
		if err := mergeFile(&cfg, ProjectConfigPath(opts.ProjectDir)); err != nil {
			return cfg, err
		}
	}
	if opts.LookupEnv != nil {
		for _, s := range settings {
			name := EnvName(s.key)
			if v, ok := opts.LookupEnv(name); ok {
				if err := s.set(&cfg, v); err != nil {
					return cfg, fmt.Errorf("env %s: %w", name, err)
				}
			}
		}
	}
	for key, v := range opts.Flags {
		s, ok := settingByKey(key)
		if !ok {
			return cfg, fmt.Errorf("unknown setting %q", key)
		}
		if err := s.set(&cfg, v); err != nil {
			return cfg, fmt.Errorf("flag --%s: %w", FlagName(key), err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("invalid configuration:\n%w", err)
	}
	return cfg, nil
}

func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// setting is one scalar that can be overridden from env or CLI.
type setting struct {
	key   string // dotted YAML path, e.g. "generator.model"
	usage string
	ptr   func(*Config) any
}

func (s setting) set(c *Config, v string) error {
	switch p := s.ptr(c).(type) {
	case *string:
		*p = v
	case *int:
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%q is not an integer", v)
		}
		*p = n
	case *float64:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("%q is not a number", v)
		}
		*p = f
	case *bool:
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("%q is not a boolean", v)
		}
		*p = b
	case *Duration:
		return p.parse(v)
	default:
		panic(fmt.Sprintf("config: unsupported setting type %T for %s", p, s.key))
	}
	return nil
}

var settings = []setting{
	{"generator.provider", "generator provider: openai|openrouter|anthropic|agent-sdk", func(c *Config) any { return &c.Generator.Provider }},
	{"generator.base_url", "generator API base URL", func(c *Config) any { return &c.Generator.BaseURL }},
	{"generator.model", "generator model name", func(c *Config) any { return &c.Generator.Model }},
	{"generator.api_key_env", "env var holding the generator API key", func(c *Config) any { return &c.Generator.APIKeyEnv }},
	{"generator.temperature", "generator sampling temperature", func(c *Config) any { return &c.Generator.Temperature }},
	{"generator.slots", "parallel generator slots (match llama-server -np)", func(c *Config) any { return &c.Generator.Slots }},
	{"generator.slot_ctx", "context tokens per slot (llama-server -c / -np)", func(c *Config) any { return &c.Generator.SlotCtx }},
	{"generator.output_reserve", "tokens reserved for the generator answer", func(c *Config) any { return &c.Generator.OutputReserve }},
	{"generator.structured", "structured output mode: json_schema|grammar|none", func(c *Config) any { return &c.Generator.Structured }},
	{"validator.provider", "validator provider: openai|openrouter|anthropic|agent-sdk|none", func(c *Config) any { return &c.Validator.Provider }},
	{"validator.base_url", "validator API base URL", func(c *Config) any { return &c.Validator.BaseURL }},
	{"validator.model", "validator model name", func(c *Config) any { return &c.Validator.Model }},
	{"validator.api_key_env", "env var holding the validator API key", func(c *Config) any { return &c.Validator.APIKeyEnv }},
	{"validator.timeout", "validator request timeout", func(c *Config) any { return &c.Validator.Timeout }},
	{"limits.max_build_fixes", "build/lint fix attempts by the generator", func(c *Config) any { return &c.Limits.MaxBuildFixes }},
	{"limits.max_reviews", "validator review rounds", func(c *Config) any { return &c.Limits.MaxReviews }},
	{"limits.max_fallback_attempts", "gate attempts for validator-written code", func(c *Config) any { return &c.Limits.MaxFallbackAttempts }},
	{"limits.max_parallel_reviews", "concurrent validator requests", func(c *Config) any { return &c.Limits.MaxParallelReviews }},
	{"limits.generate_timeout", "generation timeout", func(c *Config) any { return &c.Limits.GenerateTimeout }},
	{"limits.check_timeout", "gate timeout", func(c *Config) any { return &c.Limits.CheckTimeout }},
	{"limits.test_timeout", "test run timeout", func(c *Config) any { return &c.Limits.TestTimeout }},
	{"git.dirty", "uncommitted changes in touched files: refuse|include", func(c *Config) any { return &c.Git.Dirty }},
	{"git.branch", "commit to this branch instead of the current one", func(c *Config) any { return &c.Git.Branch }},
	{"graph.provider", "code graph: codebase-memory|graphify|noop", func(c *Config) any { return &c.Graph.Provider }},
	{"log.level", "log level: debug|info|warn|error", func(c *Config) any { return &c.Log.Level }},
	{"log.file", "also write logs to this file", func(c *Config) any { return &c.Log.File }},
}

func settingByKey(key string) (setting, bool) {
	for _, s := range settings {
		if s.key == key {
			return s, true
		}
	}
	return setting{}, false
}

// Keys lists all overridable setting keys.
func Keys() []string {
	keys := make([]string, len(settings))
	for i, s := range settings {
		keys[i] = s.key
	}
	return keys
}

// FlagName maps a key to its CLI flag: "limits.max_reviews" -> "max-reviews",
// "generator.model" -> "generator-model". Limits drop their section prefix.
func FlagName(key string) string {
	key = strings.TrimPrefix(key, "limits.")
	return strings.NewReplacer(".", "-", "_", "-").Replace(key)
}

// EnvName maps a key to its environment variable: "generator.model" -> "FOREMAN_GENERATOR_MODEL".
func EnvName(key string) string {
	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(FlagName(key), "-", "_"))
}

// RegisterFlags declares one string flag per setting on fs. After fs.Parse, call the
// returned function to get only the flags that were explicitly set, ready for Options.Flags.
func RegisterFlags(fs *flag.FlagSet) func() map[string]string {
	byFlag := make(map[string]string, len(settings))
	for _, s := range settings {
		name := FlagName(s.key)
		byFlag[name] = s.key
		fs.String(name, "", s.usage)
	}
	return func() map[string]string {
		out := map[string]string{}
		fs.Visit(func(f *flag.Flag) {
			if key, ok := byFlag[f.Name]; ok {
				out[key] = f.Value.String()
			}
		})
		return out
	}
}

// Write encodes cfg as YAML.
func Write(w io.Writer, cfg Config) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	return enc.Close()
}
