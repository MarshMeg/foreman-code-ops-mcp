# foreman-agent-sdk

Sidecar for foreman-code-ops provider `agent-sdk`: runs Claude through the
[Claude Agent SDK](https://code.claude.com/docs/en/agent-sdk/overview) and answers foreman's
requests over stdin/stdout, one JSON object per line.

```bash
# from a release (installs the foreman-agent-sdk command)
pnpm add -g https://github.com/MarshMeg/foreman-code-ops-mcp/releases/download/v0.1.2/foreman-agent-sdk-0.1.2.tgz

# from a checkout
pnpm --dir sidecar/agent-sdk pack --pack-destination /tmp
pnpm add -g /tmp/foreman-agent-sdk-0.1.2.tgz
```

`pnpm add -g ./sidecar/agent-sdk` only links the directory without installing its
dependencies; install from a packed tarball as above. foreman starts the command itself.

Authentication is the Agent SDK's: `ANTHROPIC_API_KEY`, or `CLAUDE_CODE_USE_BEDROCK`,
`CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_FOUNDRY` with that platform's credentials.

Protocol:

```text
→ {"id":1,"system":"...","messages":[{"role":"user","content":"..."}],"schema":{...},"model":"claude-opus-5-5","effort":"medium"}
← {"id":1,"text":"{...}","usage":{"prompt_tokens":0,"completion_tokens":0,"cached_tokens":0,"cost":0}}
← {"id":1,"error":"..."}
→ {"id":1,"cancel":true}     abort request 1
→ {"id":0,"ping":true}       ← {"id":0,"text":"pong"}
```

Closing stdin stops the process after running requests finish. Tests: `node --test`.
See [docs/providers.md](../../docs/providers.md#agent-sdk) for configuration.
