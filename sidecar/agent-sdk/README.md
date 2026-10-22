# foreman-agent-sdk

Sidecar for foreman-code-ops provider `agent-sdk`: runs Claude through the
[Claude Agent SDK](https://code.claude.com/docs/en/agent-sdk/overview) and answers foreman's
requests over stdin/stdout, one JSON object per line.

```bash
npm install -g ./sidecar/agent-sdk
foreman-agent-sdk          # normally started by foreman, not by hand
```

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
