# Codex CLI end-to-end tests

Runs the installed `codex` CLI in [noninteractive JSON mode](https://learn.chatgpt.com/docs/non-interactive-mode)
against OpenAI, then Wingman, using the existing local OpenAI test settings.
Each run has an isolated configuration and temporary fixture directory.

The scenarios check an exact text response and a real shell-read → apply-patch
loop. The file contains an unpredictable value; the CLI must read it, complete
an `apply_patch` edit, and produce a byte-for-byte copy. Recorded Responses API
streams must complete with usage, valid tool inputs, and matching tool results
on subsequent requests. Comparisons use these outcomes, allowing differences in
prose, request counts, IDs, token usage, and stream chunking.

## Run

Start Wingman with the models you want to test. For `task server` (port 4242):

```sh
CODEX_LIVE=1 \
WINGMAN_BASE_URL=http://localhost:4242/v1 \
CODEX_ARTIFACTS=/tmp/wingman-codex \
go test -v -count=1 -timeout 30m ./test/codex
```

Alternatively, `task test:codex` enables the live suite. Runs make paid API calls.
Each invocation is limited to two minutes and eight upstream requests, with
automatic request and stream retries disabled. Run against the Wingman build
you intend to validate.

The suite loads the repository `.env` without replacing exported values:

| Variable | Default / purpose |
| --- | --- |
| `OPENAI_API_KEY` | Required reference credential |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` |
| `TEST_OPENAI_REFERENCE_MODEL` | `gpt-5.4-mini` |
| `WINGMAN_BASE_URL` | `http://localhost:8080/v1` |
| `WINGMAN_API_KEY` | `test-key` |
| `TEST_OPENAI_MODELS` | Existing OpenAI test model matrix, comma separated |
| `CODEX_LIVE` | Set to `1` to enable paid tests; checked before loading `.env` |
| `CODEX_BIN` | `codex`; executable path or name |
| `CODEX_ARTIFACTS` | Optional directory for retained artifacts |

## Configuration and traces

The runner generates a [custom provider configuration](https://learn.chatgpt.com/docs/config-file/config-sample)
with `wire_api = "responses"` and HTTP streaming. Each child process gets its
own `CODEX_HOME` and a dummy key. The recording proxy alone holds the upstream
credential; authentication and cookie headers are excluded from artifacts.
Bodies stream to the CLI immediately while being recorded.

A generated `model_catalog_json` gives every target the same minimal fixture
instructions, shell tool, and freeform `apply_patch` capability. This is needed
because Codex's fallback metadata for unfamiliar model names omits the patch
tool. The actual model name is preserved in API requests. The suite validates
this explicit fixture configuration.

Runs use ephemeral sessions, no approval prompts, and a workspace sandbox for
edits (read-only for text). Shell network access, login shell startup, web
search, apps, hooks, subagents, remote plugins, and telemetry are disabled.
Pin the CLI version in CI; the suite logs the installed version.

Each run retains `config/config.toml`, `config/models.json`, `stdout.jsonl`,
`stderr.log`, `http.json`, and fixture files when `CODEX_ARTIFACTS` is set.
Traces contain prompts, tool input/output, and provider response bodies.
Otherwise Go removes temporary artifacts when the test ends.

Without `CODEX_LIVE=1`, the suite checks protocol validation locally and runs
the installed CLI against scripted text and read/edit APIs when available.
These offline tests make no paid provider calls.
