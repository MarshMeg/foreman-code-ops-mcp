---
name: setup
description: One-time global setup of foreman-code-ops on this machine — install or update the foreman-code-ops binary and graphify (the code graph), and configure the generator and validator models in the global config ~/.config/foreman-code-ops/config.yaml. Use when the user asks to install, set up, update or repair foreman-code-ops, to change which models foreman uses, or when `foreman-code-ops` or `graphify` is not found. To turn foreman on in a project, use the connect skill instead.
---

# Setting up foreman-code-ops (global)

foreman-code-ops lives globally: the plugin, the binary, graphify and the model settings are
shared by all projects. The plugin does **not** start the MCP server everywhere — each project
turns it on with `/foreman-code-ops:connect`. This skill prepares the machine.

Do the steps in order. Find out everything you can yourself; ask the user only for choices that
are theirs (which models, which endpoints). Talk to the user in their language; write config in
English.

**Never write API keys or tokens into any file.** Config files only name the environment variable
(`api_key_env`); tell the user which variable to set and where (shell profile).

## 1. The foreman-code-ops binary

1. Run `foreman-code-ops version`. If it works and is not older than the plugin version, go on.
2. Otherwise install the latest release:
   ```sh
   curl -fsSL https://raw.githubusercontent.com/MarshMeg/foreman-code-ops-mcp/main/scripts/install.sh | sh
   ```
   It installs into `~/.local/bin` (`FOREMAN_BIN_DIR` changes that, `FOREMAN_VERSION` pins a
   version). Release builds exist for linux and darwin, amd64 and arm64. Elsewhere, or if the
   download fails, use `go install github.com/MarshMeg/foreman-code-ops-mcp/cmd/foreman-code-ops@latest`
   (needs Go and a C compiler: tree-sitter uses cgo).
3. Check that the install directory is on PATH. If not, tell the user the line to add to their
   shell profile — Claude Code inherits PATH from the shell it was started in.

## 2. graphify (the code graph)

foreman uses [graphify](https://pypi.org/project/graphifyy/) to give Claude the structure of a
codebase (`graph_overview`, `graph_query`, `graph_node`, `graph_path`, `graph_affected`) and to
find references before deleting a symbol. foreman runs it locally on code only, without any LLM
or API key.

1. Run `graphify --version`. If it works, go on.
2. Otherwise install it (the PyPI package is `graphifyy`, with two y; Python 3.10+):
   - `uv tool install graphifyy` (preferred), then `uv tool update-shell` if `graphify` is not on PATH;
   - or `pipx install graphifyy`;
   - or, as a last resort, `python3 -m pip install --user graphifyy`.
3. Check `graphify --version` again. Do not run `graphify install` or `graphify claude install`:
   Claude reads the graph through foreman, not through graphify's own skill and hooks.

If the user explicitly prefers another graph (codebase-memory-mcp), set `graph.provider` in the
global config instead and skip this step; the graph_* tools then are not available.

## 3. Models (global config)

The global config is `~/.config/foreman-code-ops/config.yaml` (`$XDG_CONFIG_HOME` is respected).
Projects inherit it and override single keys in `.foreman/config.yaml` only when they need to.
Read the file if it exists and keep what is there. Ask the user (one question with options is
best) and write only the keys that differ from the defaults (`config.example.yaml` and
`docs/config.md` in the repository list every key; unknown keys are an error):

- **Generator** — the model that writes code:
  - a local llama-server or another OpenAI-compatible server: `provider: openai`, `base_url`
    (e.g. `http://127.0.0.1:8080/v1`), `model`, `slots` (llama-server `-np`) and `slot_ctx`
    (`-c` divided by `-np`);
  - `openrouter` or `anthropic` with a model name.
- **Validator** — reviews each change and writes `direct` code: `openrouter` (default model
  `z-ai/glm-5.3-flash`), `anthropic`, `agent-sdk` (uses the user's Claude Code login; needs the
  `foreman-agent-sdk` sidecar from the release:
  `pnpm add -g https://github.com/MarshMeg/foreman-code-ops-mcp/releases/download/v<version>/foreman-agent-sdk-<version>.tgz`)
  or `none` (nothing is reviewed).
- Hosted providers read the key from `OPENROUTER_API_KEY` / `ANTHROPIC_API_KEY` by default.
  Check whether the variable is set (`test -n "$OPENROUTER_API_KEY"`, never print it) and tell
  the user if it is missing.

## 4. Checks

1. If the generator is a llama-server, `curl -s <base_url>/models` must answer and list the
   model. Compare its `-np` and `-c` (from `ps` or `/props`) with `slots` and `slot_ctx`.
2. Run `foreman-code-ops config` (from any directory) and show the effective configuration.

## 5. Next

Tell the user that foreman is ready and how to turn it on in a project: open Claude Code in the
project and run `/foreman-code-ops:connect`.
