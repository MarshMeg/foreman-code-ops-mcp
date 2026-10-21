# Configuration

Sources, lowest to highest priority:

1. built-in defaults (see [config.example.yaml](../config.example.yaml));
2. global file `~/.config/foreman-code-ops/config.yaml` (`$XDG_CONFIG_HOME` is respected; override with `--config`);
3. project file `<project>/.foreman/config.yaml`;
4. environment variables `FOREMAN_*`;
5. CLI flags.

Files are merged key by key: a project file that sets only `limits.max_reviews` keeps everything
else from the global file. Unknown keys are an error, so typos are caught.

`foreman-code-ops config --project <dir>` prints the effective configuration.

## Environment variables and flags

Every scalar setting has a flag and an environment variable derived from its key. `limits.*`
settings drop the section prefix.

| Key | Flag | Environment |
|---|---|---|
| `generator.provider` | `--generator-provider` | `FOREMAN_GENERATOR_PROVIDER` |
| `generator.base_url` | `--generator-base-url` | `FOREMAN_GENERATOR_BASE_URL` |
| `generator.model` | `--generator-model` | `FOREMAN_GENERATOR_MODEL` |
| `generator.api_key_env` | `--generator-api-key-env` | `FOREMAN_GENERATOR_API_KEY_ENV` |
| `generator.temperature` | `--generator-temperature` | `FOREMAN_GENERATOR_TEMPERATURE` |
| `generator.slots` | `--generator-slots` | `FOREMAN_GENERATOR_SLOTS` |
| `generator.slot_ctx` | `--generator-slot-ctx` | `FOREMAN_GENERATOR_SLOT_CTX` |
| `generator.output_reserve` | `--generator-output-reserve` | `FOREMAN_GENERATOR_OUTPUT_RESERVE` |
| `generator.structured` | `--generator-structured` | `FOREMAN_GENERATOR_STRUCTURED` |
| `validator.provider` | `--validator-provider` | `FOREMAN_VALIDATOR_PROVIDER` |
| `validator.base_url` | `--validator-base-url` | `FOREMAN_VALIDATOR_BASE_URL` |
| `validator.model` | `--validator-model` | `FOREMAN_VALIDATOR_MODEL` |
| `validator.api_key_env` | `--validator-api-key-env` | `FOREMAN_VALIDATOR_API_KEY_ENV` |
| `validator.timeout` | `--validator-timeout` | `FOREMAN_VALIDATOR_TIMEOUT` |
| `limits.max_build_fixes` | `--max-build-fixes` | `FOREMAN_MAX_BUILD_FIXES` |
| `limits.max_reviews` | `--max-reviews` | `FOREMAN_MAX_REVIEWS` |
| `limits.max_fallback_attempts` | `--max-fallback-attempts` | `FOREMAN_MAX_FALLBACK_ATTEMPTS` |
| `limits.max_parallel_reviews` | `--max-parallel-reviews` | `FOREMAN_MAX_PARALLEL_REVIEWS` |
| `limits.generate_timeout` | `--generate-timeout` | `FOREMAN_GENERATE_TIMEOUT` |
| `limits.check_timeout` | `--check-timeout` | `FOREMAN_CHECK_TIMEOUT` |
| `limits.test_timeout` | `--test-timeout` | `FOREMAN_TEST_TIMEOUT` |
| `git.dirty` | `--git-dirty` | `FOREMAN_GIT_DIRTY` |
| `git.branch` | `--git-branch` | `FOREMAN_GIT_BRANCH` |
| `graph.provider` | `--graph-provider` | `FOREMAN_GRAPH_PROVIDER` |
| `log.level` | `--log-level` | `FOREMAN_LOG_LEVEL` |
| `log.file` | `--log-file` | `FOREMAN_LOG_FILE` |

Durations use Go syntax: `90s`, `5m`, `1h30m`.

## Per-call limits

`upsert_symbol` accepts an optional `limits` object with `max_build_fixes`, `max_reviews`,
`max_fallback_attempts`, `generate_timeout`, `check_timeout` and `test_timeout`. Omitted fields keep
the configured values.

## Runtime files

| Path | Purpose | Commit it? |
|---|---|---|
| `.foreman/config.yaml` | project settings | yes |
| `.foreman/style.md` | project style guide | yes |
| `.foreman/cache/journal.jsonl` | log of every tool call | no |
