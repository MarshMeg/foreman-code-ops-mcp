#!/usr/bin/env node
// foreman-code-ops Agent SDK sidecar. Reads one JSON request per line on stdin and writes one
// JSON response per line on stdout; requests run concurrently and answers carry the request id.
//
//   → {"id":1,"system":"...","messages":[{"role":"user","content":"..."}],"schema":{...},"model":"claude-opus-5-5"}
//   ← {"id":1,"text":"{...}","usage":{"prompt_tokens":0,"completion_tokens":0,"cached_tokens":0,"cost":0}}
//   ← {"id":1,"error":"..."}
//
// {"id":0,"ping":true} is answered with {"id":0,"text":"pong"} once the SDK is loaded;
// {"id":1,"cancel":true} aborts request 1, which then answers with an error.

import { createInterface } from "node:readline";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { query } from "@anthropic-ai/claude-agent-sdk";
import { handle } from "./lib.mjs";

// Queries run in an empty directory: the agent has no tools, and nothing from the project leaks in.
const cwd = mkdtempSync(join(tmpdir(), "foreman-agent-sdk-"));
const env = { ...process.env, CLAUDE_AGENT_SDK_CLIENT_APP: "foreman-code-ops" };
const pending = new Set();
const running = new Map(); // id -> AbortController

function send(obj) {
  process.stdout.write(JSON.stringify(obj) + "\n");
}

const rl = createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on("line", (line) => {
  if (!line.trim()) {
    return;
  }
  let req;
  try {
    req = JSON.parse(line);
  } catch (err) {
    send({ id: null, error: `invalid request: ${err.message}` });
    return;
  }
  if (req.ping) {
    send({ id: req.id, text: "pong" });
    return;
  }
  if (req.cancel) {
    running.get(req.id)?.abort();
    return;
  }
  const ac = new AbortController();
  running.set(req.id, ac);
  const job = handle(req, query, { cwd, env, signal: ac.signal })
    .catch((err) => ({ id: req.id, error: String(err?.message ?? err) }))
    .then(send)
    .finally(() => {
      pending.delete(job);
      running.delete(req.id);
    });
  pending.add(job);
});
// foreman closes stdin to stop the sidecar; finish what is running first.
rl.on("close", async () => {
  await Promise.allSettled([...pending]);
  process.exit(0);
});
