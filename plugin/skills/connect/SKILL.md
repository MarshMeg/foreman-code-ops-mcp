---
name: connect
description: Connect foreman-code-ops to the current project — run foreman-code-ops init (project MCP server in .mcp.json, deny rules that route source edits through foreman, the foreman section in CLAUDE.md, the graphify code graph), review the services in .foreman/workspace.yaml, check the gate's tools, commit, and continue in a new session where the foreman MCP server is loaded. Use when the user wants to use foreman, the foreman MCP or its code graph in this project, or when the project has no .foreman directory or no foreman entry in .mcp.json.
---

# Connecting a project to foreman-code-ops

The plugin, the binary, graphify and the model settings are global; this skill turns foreman on
for one project. The foreman MCP server is registered in the project's `.mcp.json`, so it starts
only in this project, and only in sessions started after it was registered.

Talk to the user in their language; write config and commit messages in English.

## 1. Prerequisites

Run `foreman-code-ops version` and `graphify --version`, and check that
`~/.config/foreman-code-ops/config.yaml` exists (or `$XDG_CONFIG_HOME/foreman-code-ops/`). If
anything is missing, do the setup skill (`/foreman-code-ops:setup`) first, then come back.

The project must be a git repository (foreman commits every change). If it is not, ask before
running `git init`. Note uncommitted changes in `CLAUDE.md`, `.gitignore`, `.mcp.json` and
`.claude/settings.json` (`git status --short`): init edits these files.

## 2. Init

Run `foreman-code-ops init --project .`. It merges into existing files and never replaces them:

- `.foreman/workspace.yaml` — services and their languages (kept if it exists; `--force`
  regenerates it — ask first, the user may have edited it);
- `.claude/settings.json` — `permissions.deny` rules for source files (edits go through foreman)
  and `enabledMcpjsonServers: ["foreman"]`, so the server starts without a prompt;
- `.mcp.json` — the `foreman` server: `foreman-code-ops --project ${CLAUDE_PROJECT_DIR:-.}`;
- `CLAUDE.md` — a section between `<!-- foreman-code-ops:begin -->` and `<!-- foreman-code-ops:end -->`:
  read the code through foreman's graph tools, write it through foreman's tools;
- `.gitignore` — `.foreman/cache/` and `graphify-out/`;
- `graphify-out/` — the code graph, built now (code only, no LLM; seconds for most projects).

Show the user the report lines. A warning that the graph was not built is not fatal: foreman
builds it on the first graph query.

## 3. Review

1. Read `.foreman/workspace.yaml` and compare it with the repository: every service root and its
   language, nothing generated or vendored, and which service calls which. Fix it.
2. Model settings come from the global config. Create `.foreman/config.yaml` only for keys this
   project needs differently (for example another validator) — ask the user. Never write API keys.
3. Check the gate's tools for the project's languages and list what is missing, with install
   commands; prefer the project's own package manager and dev dependencies:
   - Go: `go`, `goimports`, optionally `gopls`;
   - Python: `ruff`, `pyright` or `mypy`, `pytest`;
   - TypeScript / JavaScript: `tsc`, `biome` or `prettier` + `eslint`, `vitest` or `jest`;
   - Rust: `cargo`, `rustfmt`; C#: `dotnet`.
4. Run `foreman-code-ops config --project .` and check the effective configuration.

## 4. Commit

Show the user what changed and commit these files in a separate commit, for example
`foreman: connect foreman-code-ops`: `.foreman/workspace.yaml` (and `.foreman/config.yaml` if
created), `.claude/settings.json`, `.mcp.json`, `CLAUDE.md`, `.gitignore`. Leave other
uncommitted changes of the user alone; never commit `graphify-out/` or `.foreman/cache/`.

## 5. Continue in a new session

The foreman tools appear only in a session started after `.mcp.json` was written, while the deny
rules already apply. Do not change source code in this session any more.

- If you can start a new Claude Code session yourself (for example a start-session tool in the
  Claude desktop app), start one in this project's folder with this prompt:
  > foreman-code-ops was just connected to this project. Call the foreman `status` tool and fix the
  > warnings it reports. Then call `graph_overview` and summarise the project's structure for me.
  > If `style_get` reports no style guide, study the code and offer to write one with `style_set`.
- Otherwise tell the user to end this session and start Claude Code again in the project
  (`claude` in its directory, or a new session in the app), then ask it to call `status`.

Tell the user what you did and that the new session is where foreman is active.
