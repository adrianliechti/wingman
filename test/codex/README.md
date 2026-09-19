# Codex CLI end-to-end tests

Runs the installed `codex` CLI in [noninteractive JSON mode](https://learn.chatgpt.com/docs/non-interactive-mode)
against OpenAI, then Wingman, using the existing local OpenAI test settings.
Each run has an isolated configuration and temporary fixture directory.

The scenarios check an exact text response, a shell-read → apply-patch copy
with unpredictable input, and a project repair shared with the Claude Code
suite. The project scenario requires Codex to observe failing Python tests,
read five files, patch both source modules with `apply_patch`, and rerun all
seven tests successfully. The harness checks command exit codes and file-change
events, protects the tests and input data, and independently reruns the tests.
It also checks recorded command arguments so fabricated output or shell edits
cannot satisfy the workflow.

Recorded Responses API streams must complete with usage, valid tool inputs,
and matching tool results on subsequent requests. Comparisons use these
outcomes, allowing differences in prose, request counts, IDs, token usage,
and stream chunking.

## Run

Start Wingman with the models you want to test. For `task server` (port 4242):

```sh
CODEX_LIVE=1 \
WINGMAN_BASE_URL=http://localhost:4242/v1 \
CODEX_ARTIFACTS=/tmp/wingman-codex \
go test -v -count=1 -timeout 30m ./test/codex
```

Alternatively, `task test:codex` enables the live suite. Runs make paid API calls.
Text and copy runs are limited to two minutes and eight upstream requests;
project repair gets three minutes and twelve requests. Automatic request and
stream retries are disabled. Python 3 is required for project repair. Run
against the Wingman build you intend to validate.

To select only project repair and specific configured models:

```sh
CODEX_LIVE=1 \
WINGMAN_BASE_URL=http://localhost:4242/v1 \
TEST_OPENAI_MODELS=gpt-5.4,claude-sonnet-4-6,claude-opus-5 \
go test -v -count=1 -timeout 30m ./test/codex -run '^TestCodex$/project_repair'
```

The default matrix adds `claude-opus-5` to the existing OpenAI test matrix.
Unconfigured defaults are skipped. Explicitly requested models missing from
Wingman's model listing fail before the paid reference run.

The suite loads the repository `.env` without replacing exported values:

| Variable | Default / purpose |
| --- | --- |
| `OPENAI_API_KEY` | Required reference credential |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` |
| `TEST_OPENAI_REFERENCE_MODEL` | `gpt-5.4-mini` |
| `WINGMAN_BASE_URL` | `http://localhost:8080/v1` |
| `WINGMAN_API_KEY` | `test-key` |
| `TEST_OPENAI_MODELS` | Override the default matrix, comma separated |
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
Project runs also retain the independent test output in `verification.log`.
Traces contain prompts, tool input/output, and provider response bodies.
Otherwise Go removes temporary artifacts when the test ends.

Without `CODEX_LIVE=1`, the suite checks protocol validation locally and runs
the installed CLI against scripted text, read/edit, and project-repair APIs
when available. These offline tests make no paid provider calls. The project
test exercises a real failing command, a patch across two files, and a passing
rerun; negative validator tests reject missing or unsuccessful repair steps.

The suite covers bounded, single-session workflows. Resume, compaction, and
long-running interactive sessions are not exercised.

## Hard scenarios

`TestCodexHard` covers what the base scenarios avoid, with the same
reference-then-Wingman comparison:

- `parallel_shell`: two shell reads the model may issue in one turn.
- `view_image`: the CLI attaches a PNG for the model through `view_image`.
- `web_search`: Codex configured with `web_search = "live"`, which adds the
  hosted `web_search` tool to every request.
- `auto_compact`: the project repair with `model_auto_compact_token_limit`
  low enough that Codex compacts the conversation mid-task. Compaction is
  client-side: Codex asks for a handoff summary and continues in a fresh
  thread, all through ordinary Responses requests.

Every run logs the wire features it used (request parameters, tool types,
input item types, answered item types and statuses).

## Upstream recording

`TestCodexUpstream` runs Wingman in-process between Codex and OpenAI with
both hops recorded. It reports which client features the gateway dropped or
rewrote on the way upstream, requires the upstream request prefix
(instructions, tools, reasoning, replayed input) to stay byte-stable from
turn to turn, and requires OpenAI to report cached input tokens on every
turn after the first. Set `CODEX_ARTIFACTS` to retain the upstream
exchanges as `<test>-upstream.json`.
