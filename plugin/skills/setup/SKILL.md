---
name: setup
description: Set up foreman-code-ops for the current project — install or update the foreman-code-ops binary, run init, review .foreman/workspace.yaml, configure the generator and validator, check llama-server and the gate's tools, write a style guide and commit the result. Use when the user asks to install, set up, configure or repair foreman-code-ops, when the foreman MCP server fails to start (foreman-code-ops not found), or when the project has no .foreman directory yet.
---

# Setting up foreman-code-ops

Do the steps in order. Find out everything you can yourself; ask the user only for choices
that are theirs (which models, which endpoints). Talk to the user in their language; write
config and the style guide in English.

**Never write API keys or tokens into any file.** Config files only name the environment
variable (`api_key_env`); tell the user which variable to set and where (shell profile).

## 1. Binary

The plugin starts `foreman-code-ops` from PATH.

1. Run `foreman-code-ops version`. If it works and matches the plugin version (or is newer),
   skip to step 2.
2. Otherwise install the latest release:
   ```sh
   curl -fsSL https://raw.githubusercontent.com/MarshMeg/foreman-code-ops-mcp/main/scripts/install.sh | sh
   ```
   It installs into `~/.local/bin` (`FOREMAN_BIN_DIR` changes that, `FOREMAN_VERSION` pins a
   version). Release builds exist for linux and darwin, amd64 and arm64. On other platforms, or
   if the download fails, use
   `go install github.com/MarshMeg/foreman-code-ops-mcp/cmd/foreman-code-ops@latest` (needs Go
   and a C compiler: tree-sitter uses cgo).
3. Check that the install directory is on PATH. If not, tell the user the line to add to their
   shell profile — Claude Code inherits PATH from the shell it was started in.

## 2. Project files

1. The project must be a git repository (foreman commits every change). If it is not, ask
   before running `git init`.
2. Run `foreman-code-ops init --project .`. It detects services and languages, writes
   `.foreman/workspace.yaml` and adds `permissions.deny` rules for source files to
   `.claude/settings.json`. If `.foreman/workspace.yaml` already exists, do not pass `--force`
   without asking: the user may have edited it. Do not pass `--mcp`: the plugin registers the
   server.
3. Read `.foreman/workspace.yaml` and compare it with the repository: every service root and
   its language, nothing generated or vendored, and which service calls which. Fix it.

## 3. Models

Ask the user (one question with options is best) and write the answers to
`.foreman/config.yaml` — only the keys that differ from the defaults
(`config.example.yaml` and `docs/config.md` in the repository list every key). Unknown keys
are an error.

- **Generator** — the model that writes code:
  - a local llama-server or other OpenAI-compatible server: `provider: openai`, `base_url`
    (e.g. `http://127.0.0.1:8080/v1`), `model`, `slots` (llama-server `-np`) and `slot_ctx`
    (`-c` divided by `-np`);
  - `openrouter` or `anthropic` with a model name.
- **Validator** — reviews each change and writes `direct` code: `openrouter` (default model
  `z-ai/glm-5.3-flash`), `anthropic`, `agent-sdk` (uses the user's Claude Code login; needs the
  `foreman-agent-sdk` sidecar from the release: `pnpm add -g <url of foreman-agent-sdk-<version>.tgz>`)
  or `none` (nothing is reviewed).
- Hosted providers read the key from `OPENROUTER_API_KEY` / `ANTHROPIC_API_KEY` by default.
  Check whether the variable is set (`test -n "$OPENROUTER_API_KEY"`, never print it) and tell
  the user if it is missing.

Settings shared by all projects (endpoints, models) may instead go to the global file
`~/.config/foreman-code-ops/config.yaml` — ask the user which they prefer when they have several
projects.

## 4. Checks

1. If the generator is a llama-server, `curl -s <base_url>/models` must answer and list the
   model. Compare its `-np` and `-c` (from `ps` or `/props`) with `slots` and `slot_ctx`.
2. Check the gate's tools for the project's languages and list what is missing, with install
   commands:
   - Go: `go`, `goimports`, optionally `gopls`;
   - Python: `ruff`, `pyright` or `mypy`, `pytest`;
   - TypeScript / JavaScript: `tsc` (project `node_modules` or global), `biome` or
     `prettier` + `eslint`, `vitest` or `jest`;
   - Rust: `cargo`, `rustfmt`; C#: `dotnet`.
   Prefer the project's own package manager and dev dependencies over global installs.
3. Optional: `lsp.enabled: true` makes the gate check changed files with a language server
   first (faster for Python and TypeScript); the servers are listed in `docs/adapters.md`.
4. Run `foreman-code-ops config --project .` and show the effective configuration.

## 5. Restart and verify

Tell the user to restart Claude Code (or run `/mcp` to reconnect) so the server picks up the
binary and config. After that, call the `status` tool and fix each warning it reports. If the
foreman tools are not available yet, stop here and tell the user to continue after the restart.

## 6. Style guide

If `style_get` reports no style guide, study the codebase (errors, logging, constructors,
layer naming, context, comments, per-language conventions) and write a short one with
`style_set`. The `foreman-code-ops` skill lists the sections.

## 7. Commit

Show the user what changed (`.foreman/workspace.yaml`, `.foreman/config.yaml`,
`.foreman/style.md`, `.claude/settings.json`, `.gitignore`) and commit it in a separate commit, for example
`foreman: set up foreman-code-ops`. init adds `.foreman/cache/` to `.gitignore`; never commit
the cache.
