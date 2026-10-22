// Command foreman-code-ops is an MCP server that delegates writing code to a cheaper
// model and verifies, applies and commits the result symbol by symbol.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/bench"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/config"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/gitops"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/journal"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/lang/builtin"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/logging"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/mcp"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/ops"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/project"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/providers"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/setup"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/stats"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "foreman-code-ops:", err)
		}
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	cmd := "serve"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(args, stderr)
	case "version":
		fmt.Println(version)
		return nil
	case "config":
		return printConfig(args, stderr)
	case "init":
		return initProject(args, stderr)
	case "bench-review":
		return benchReview(args, stderr)
	case "stats":
		return printStats(args, os.Stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q (commands: serve, init, config, stats, bench-review, version)", cmd)
	}
}

// commonFlags parses --project, --config and setting overrides, then loads the configuration.
func commonFlags(name string, args []string, stderr io.Writer) (config.Config, string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", ".", "project root")
	globalPath := fs.String("config", "", "global config file (default ~/.config/foreman-code-ops/config.yaml)")
	setFlags := config.RegisterFlags(fs)
	if err := fs.Parse(args); err != nil {
		return config.Config{}, "", err
	}
	if fs.NArg() > 0 {
		return config.Config{}, "", fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	dir, err := filepath.Abs(*project)
	if err != nil {
		return config.Config{}, "", err
	}
	if *globalPath == "" {
		if p, err := config.GlobalConfigPath(); err == nil {
			*globalPath = p
		}
	}
	cfg, err := config.Load(config.Options{
		GlobalPath: *globalPath,
		ProjectDir: dir,
		LookupEnv:  os.LookupEnv,
		Flags:      setFlags(),
	})
	return cfg, dir, err
}

func serve(args []string, stderr io.Writer) error {
	cfg, dir, err := commonFlags("serve", args, stderr)
	if err != nil {
		return err
	}
	logger, closeLog, err := logging.New(cfg.Log.Level, cfg.Log.File)
	if err != nil {
		return err
	}
	defer func() { _ = closeLog() }()

	j, err := journal.Open(filepath.Join(dir, config.DirName, "cache", "journal.jsonl"))
	if err != nil {
		logger.Warn("journal disabled", "err", err)
		j = journal.Discard()
	}
	defer func() { _ = j.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting", "version", version, "project", dir,
		"generator", cfg.Generator.Provider+"/"+cfg.Generator.Model,
		"validator", cfg.Validator.Provider+"/"+cfg.Validator.Model)
	proj, err := project.Open(dir)
	if err != nil {
		return err
	}
	reg, own, err := builtin.RegistryFor(proj.Root)
	if err != nil {
		return err
	}
	if len(own) > 0 {
		logger.Info("project adapters loaded", "langs", own)
	}
	o, err := ops.New(proj, reg, cfg, logger)
	if err != nil {
		return err
	}
	defer o.Close()
	s := mcp.NewServer(mcp.Deps{Version: version, ProjectDir: proj.Root, Config: cfg, Logger: logger, Journal: j, Ops: o})
	if err := s.Run(ctx, &sdk.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// printConfig prints the effective merged configuration as YAML.
func printConfig(args []string, stderr io.Writer) error {
	cfg, _, err := commonFlags("config", args, stderr)
	if err != nil {
		return err
	}
	return config.Write(os.Stdout, cfg)
}

// initProject prepares a project: services draft, Claude Code deny rules for files foreman
// edits, optional .mcp.json entry, and a .gitignore line for runtime files.
func initProject(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("project", ".", "project root")
	force := fs.Bool("force", false, "regenerate an existing .foreman/workspace.yaml")
	noPerms := fs.Bool("no-permissions", false, "do not add permissions.deny rules to .claude/settings.json")
	mcpEntry := fs.Bool("mcp", false, "register the server in the project's .mcp.json (not needed with the plugin)")
	binary := fs.String("binary", "foreman-code-ops", "command written to .mcp.json")
	extFlag := fs.String("extensions", "", "comma-separated extensions to route through foreman (default: by detected languages)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	exts, err := initExtensions(root, *extFlag)
	if err != nil {
		return err
	}
	rep, err := setup.Init(setup.Options{Root: root, Extensions: exts, Permissions: !*noPerms,
		MCP: *mcpEntry, Binary: *binary, ForceWorkspace: *force})
	for _, l := range rep.Lines {
		fmt.Fprintln(stderr, l)
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(stderr, "next: review and commit these files; ask Claude to create .foreman/style.md with style_set")
	return nil
}

// initExtensions picks the extensions whose direct edits are denied: the flag, or the
// languages found in workspace.yaml and by service markers.
func initExtensions(root, flagValue string) ([]string, error) {
	if flagValue != "" {
		var exts []string
		for _, e := range strings.Split(flagValue, ",") {
			if e = strings.TrimSpace(e); e != "" {
				if !strings.HasPrefix(e, ".") {
					e = "." + e
				}
				exts = append(exts, e)
			}
		}
		return exts, nil
	}
	var langs []string
	if p, err := project.Open(root); err == nil {
		for _, s := range p.Workspace.Services {
			langs = append(langs, s.Lang)
		}
	}
	svcs, err := project.Discover(root)
	if err != nil {
		return nil, err
	}
	for _, s := range svcs {
		langs = append(langs, s.Lang)
	}
	return setup.ExtensionsFor(langs), nil
}

// benchReview measures validator latency, cache hits and cost on a fixed review (plan §7a).
func benchReview(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("bench-review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	provider := fs.String("provider", "openrouter", "openrouter | openai | anthropic")
	baseURL := fs.String("base-url", "", "API base URL (default per provider)")
	models := fs.String("models", "z-ai/glm-5.3-flash,z-ai/glm-5.3-flashx", "comma-separated models")
	parallel := fs.String("parallel", "1,4,8", "comma-separated parallelism levels")
	n := fs.Int("n", 16, "requests per model and level")
	keyEnv := fs.String("api-key-env", "", "env var with the API key (default per provider)")
	timeout := fs.Duration("timeout", 120*time.Second, "per-request timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var levels []int
	for _, s := range strings.Split(*parallel, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || v < 1 {
			return fmt.Errorf("invalid parallelism %q", s)
		}
		levels = append(levels, v)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var results []bench.Result
	for _, m := range strings.Split(*models, ",") {
		p, err := providers.New(providers.Spec{Provider: *provider, BaseURL: *baseURL, Model: strings.TrimSpace(m),
			APIKeyEnv: *keyEnv, Structured: providers.StructuredJSONSchema, Timeout: *timeout})
		if err != nil {
			return err
		}
		if p == nil {
			return errors.New("provider none cannot be benchmarked")
		}
		for _, lvl := range levels {
			fmt.Fprintf(stderr, "%s, parallel %d: %d requests...\n", p.Name(), lvl, *n)
			results = append(results, bench.Run(ctx, p, *n, lvl))
		}
	}
	bench.WriteMarkdown(os.Stdout, results)
	return nil
}

// printStats reports what foreman did, from the local journal and the git trailers.
func printStats(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("project", ".", "project root")
	sinceFlag := fs.String("since", "", "only work since a date (2026-10-01) or a duration ago (7d, 12h)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	since, err := parseSince(*sinceFlag, time.Now())
	if err != nil {
		return err
	}
	rep := stats.Report{Since: since}
	if rep.Journal, err = stats.ReadJournal(filepath.Join(root, config.DirName, "cache", "journal.jsonl"), since); err != nil {
		return err
	}
	ctx := context.Background()
	if repo, err := gitops.Open(ctx, root); err == nil {
		commits, err := repo.ForemanCommits(ctx, since)
		if err != nil {
			return err
		}
		rep.Git = stats.SummarizeCommits(commits)
	} else {
		fmt.Fprintln(stderr, "git history skipped:", err)
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	stats.WriteText(stdout, rep)
	return nil
}

// parseSince accepts "", a date, an RFC 3339 time or a duration with a d (days) unit.
func parseSince(v string, now time.Time) (time.Time, error) {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return time.Time{}, nil
	case strings.HasSuffix(v, "d"):
		if n, err := strconv.Atoi(strings.TrimSuffix(v, "d")); err == nil && n >= 0 {
			return now.AddDate(0, 0, -n), nil
		}
	default:
		if d, err := time.ParseDuration(v); err == nil {
			return now.Add(-d), nil
		}
		for _, layout := range []string{time.DateOnly, time.RFC3339} {
			if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
				return t, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("invalid --since %q: use a date (2026-10-01) or a duration (7d, 12h)", v)
}
