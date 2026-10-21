// Command foreman-code-ops is an MCP server that delegates writing code to a cheaper
// model and verifies, applies and commits the result symbol by symbol.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MarshMeg/foreman-code-ops/internal/config"
	"github.com/MarshMeg/foreman-code-ops/internal/journal"
	"github.com/MarshMeg/foreman-code-ops/internal/lang/builtin"
	"github.com/MarshMeg/foreman-code-ops/internal/logging"
	"github.com/MarshMeg/foreman-code-ops/internal/mcp"
	"github.com/MarshMeg/foreman-code-ops/internal/ops"
	"github.com/MarshMeg/foreman-code-ops/internal/project"
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
	default:
		return fmt.Errorf("unknown command %q (commands: serve, init, config, version)", cmd)
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
	defer closeLog()

	j, err := journal.Open(filepath.Join(dir, config.DirName, "cache", "journal.jsonl"))
	if err != nil {
		logger.Warn("journal disabled", "err", err)
		j = journal.Discard()
	}
	defer j.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting", "version", version, "project", dir,
		"generator", cfg.Generator.Provider+"/"+cfg.Generator.Model,
		"validator", cfg.Validator.Provider+"/"+cfg.Validator.Model)
	proj, err := project.Open(dir)
	if err != nil {
		return err
	}
	reg, err := builtin.Registry()
	if err != nil {
		return err
	}
	o, err := ops.New(proj, reg, cfg, logger)
	if err != nil {
		return err
	}
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

// initProject writes a draft .foreman/workspace.yaml from discovered service roots.
func initProject(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("project", ".", "project root")
	force := fs.Bool("force", false, "overwrite an existing workspace.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	path := project.WorkspacePath(root)
	if _, err := os.Stat(path); err == nil && !*force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", path)
	}
	svcs, err := project.Discover(root)
	if err != nil {
		return err
	}
	data, err := project.MarshalWorkspace(project.Workspace{Services: svcs})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "wrote %s with %d service(s); review it and commit it\n", path, len(svcs))
	os.Stdout.Write(data)
	return nil
}
