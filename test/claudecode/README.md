# Claude Code end-to-end tests

Runs the installed `claude` CLI in [print mode](https://code.claude.com/docs/en/headless)
against Anthropic, then Wingman. The reference runs once per scenario. Each run
has its own temporary working directory and Claude configuration.

The scenarios check an exact text response, a Read → Edit tool loop that
copies an unpredictable fixture into a placeholder file, and a project repair.
The project scenario starts with two broken Python modules and seven tests:
the CLI must run the tests and observe failures, inspect the files, edit both
modules, and rerun the tests successfully. The harness checks the recorded
failure/edit/success sequence, protects the tests and input fixtures from edits,
and independently runs the final tests. Python 3 is required for this scenario.
Both endpoints must
complete the task, return valid SSE lifecycles and usage, produce valid streamed tool
arguments, and accept matching tool results on subsequent requests. Comparison
uses the final fixture/text and completed tool names; request counts, generated
prose, IDs, token counts, and chunk boundaries may differ.
Models may recover from local tool errors. Each required tool must succeed,
all tool results must match recorded calls, and the final file must be correct.

## Run

Start Wingman with the models you want to test. For `task server` (port 4242):

```sh
CLAUDE_CODE_LIVE=1 \
WINGMAN_BASE_URL=http://localhost:4242/v1 \
TEST_ANTHROPIC_MODELS=claude-sonnet-4-6,claude-opus-5 \
CLAUDE_CODE_ARTIFACTS=/tmp/wingman-claude-code \
go test -v -count=1 -timeout 30m ./test/claudecode
```

Alternatively, `task test:claudecode` enables the live suite. These runs make
paid API calls. Text and copy scenarios are limited to six turns and two minutes;
project repair allows twelve turns and three minutes. Every invocation has a
$1 CLI budget. Run against the Wingman build you intend to validate. The live
CLI suite is separate from `task test`; it must be invoked explicitly.

For Claude over Bedrock, include an Opus model such as `claude-opus-5` in
`TEST_ANTHROPIC_MODELS` alongside `claude-sonnet-4-6`. Newer Claude Code sends
mid-conversation system messages for Opus; Converse requires those instructions
in its top-level system field. The model names must route to Bedrock in your
Wingman configuration.

The suite reuses the existing Anthropic test settings and loads the repository
`.env` without overriding exported values:

| Variable | Default / purpose |
| --- | --- |
| `ANTHROPIC_API_KEY` | Required reference credential |
| `ANTHROPIC_BASE_URL` | `https://api.anthropic.com/v1` |
| `TEST_ANTHROPIC_REFERENCE_MODEL` | `claude-sonnet-4-6` |
| `WINGMAN_BASE_URL` | `http://localhost:8080/v1` |
| `WINGMAN_API_KEY` | `test-key` |
| `TEST_ANTHROPIC_MODELS` | Comma-separated models; defaults to the Anthropic test matrix plus `claude-opus-5` |
| `CLAUDE_CODE_LIVE` | Set to `1` to enable paid tests; checked before loading `.env` |
| `CLAUDE_CODE_BIN` | `claude`; executable path or name |
| `CLAUDE_CODE_ARTIFACTS` | Optional directory for retained artifacts |

Missing models in the default matrix are skipped. A model explicitly selected
with `TEST_ANTHROPIC_MODELS` must appear in Wingman's model listing when that
listing is available, or the suite fails before making reference calls.

Pin the Claude Code version in CI; the suite logs the version. The runner uses
[`--bare`, tool allowlists and `--permission-mode dontAsk`](https://code.claude.com/docs/en/cli-reference)
to avoid personal project instructions, plugins and permission prompts. Only
Read and Edit are available for the copy scenario. Project repair also exposes
Bash with an allow rule for exactly `python3 -m unittest -v`; the CLI receives
the same isolated environment without the caller's provider credentials.

## Inspect calls

A local forwarding proxy records actual HTTP requests and streams for both
targets without buffering responses before delivery. It handles the difference
between the existing `/v1` test base URLs and Claude Code's root base URL.
The CLI receives a dummy API key; the proxy adds the target's real credential.
Only an allowlist of protocol headers is recorded.

Each run writes `http.json`, `stdout.jsonl`, `stderr.log`, and the fixture files.
Project repair also writes `verification.log` from the independent test run.
Set `CLAUDE_CODE_ARTIFACTS` to retain them after the test; otherwise Go removes
them with the test's temporary directory. Treat retained traces as test data:
they contain request/response bodies, prompts and tool results. Each endpoint
has its own conversation, keeping signatures associated with their origin.

Without `CLAUDE_CODE_LIVE=1`, `go test ./test/claudecode` exercises the recorder
and stream checks locally, and runs the installed CLI against a scripted local
API when available. It makes no paid provider calls.
The Bedrock regression runs the CLI through Wingman's real handler and Bedrock
adapter with a scripted Converse transport, checking trailing system instructions
and the CLI's keep-all thinking setting.

## Hard scenarios

`TestClaudeCodeHard` covers what the base scenarios avoid, with the same
reference-then-Wingman comparison:

- `default_tools`: the CLI's built-in tool set instead of an explicit list.
- `image_read`: `Read` on a PNG, so the tool result carries an image.
- `thinking_repair`: the project repair at high effort, so thinking blocks
  are produced and replayed on every turn; the run checks each replayed
  block is signed.

Every run logs the wire features it used (request parameters, tool types,
betas, cache breakpoints, answered block types and stop reasons), so the
reference and Wingman logs can be diffed for what the CLI relies on.

## Upstream recording

`TestClaudeCodeUpstream` runs Wingman in-process between Claude Code and
Anthropic with both hops recorded. It reports which client features the
gateway dropped or rewrote on the way upstream, requires the upstream
request prefix (system, tools, thinking, replayed history) to stay
byte-stable from turn to turn, and requires Anthropic to report a prompt
cache read on every turn after the first. Set `CLAUDE_CODE_ARTIFACTS` to
retain the upstream exchanges as `<test>-upstream.json`.
