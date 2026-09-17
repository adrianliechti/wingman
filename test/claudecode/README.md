# Claude Code end-to-end tests

Runs the installed `claude` CLI in [print mode](https://code.claude.com/docs/en/headless)
against Anthropic, then Wingman. The reference runs once per scenario. Each run
has its own temporary working directory and Claude configuration.

The scenarios check an exact text response and a Read → Edit tool loop that
copies an unpredictable fixture into a placeholder file. Both endpoints must
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
TEST_ANTHROPIC_MODELS=claude-sonnet-4-6 \
CLAUDE_CODE_ARTIFACTS=/tmp/wingman-claude-code \
go test -v -count=1 -timeout 30m ./test/claudecode
```

Alternatively, `task test:claudecode` enables the live suite. These runs make
paid API calls. Each CLI invocation is limited to six turns, two minutes and
a $1 CLI budget. Run against the Wingman build you intend to validate.

The suite reuses the existing Anthropic test settings and loads the repository
`.env` without overriding exported values:

| Variable | Default / purpose |
| --- | --- |
| `ANTHROPIC_API_KEY` | Required reference credential |
| `ANTHROPIC_BASE_URL` | `https://api.anthropic.com/v1` |
| `TEST_ANTHROPIC_REFERENCE_MODEL` | `claude-sonnet-4-6` |
| `WINGMAN_BASE_URL` | `http://localhost:8080/v1` |
| `WINGMAN_API_KEY` | `test-key` |
| `TEST_ANTHROPIC_MODELS` | Existing Anthropic test model matrix, comma separated |
| `CLAUDE_CODE_LIVE` | Set to `1` to enable paid tests; checked before loading `.env` |
| `CLAUDE_CODE_BIN` | `claude`; executable path or name |
| `CLAUDE_CODE_ARTIFACTS` | Optional directory for retained artifacts |

Pin the Claude Code version in CI; the suite logs the version. The runner uses
[`--bare`, tool allowlists and `--permission-mode dontAsk`](https://code.claude.com/docs/en/cli-reference)
to avoid personal project instructions, plugins and permission prompts. Only
Read and Edit are available for the fixture scenario. No shell tool is enabled.

## Inspect calls

A local forwarding proxy records actual HTTP requests and streams for both
targets without buffering responses before delivery. It handles the difference
between the existing `/v1` test base URLs and Claude Code's root base URL.
The CLI receives a dummy API key; the proxy adds the target's real credential.
Only an allowlist of protocol headers is recorded.

Each run writes `http.json`, `stdout.jsonl`, `stderr.log`, and the fixture files.
Set `CLAUDE_CODE_ARTIFACTS` to retain them after the test; otherwise Go removes
them with the test's temporary directory. Treat retained traces as test data:
they contain request/response bodies, prompts and tool results. Each endpoint
has its own conversation, keeping signatures associated with their origin.

Without `CLAUDE_CODE_LIVE=1`, `go test ./test/claudecode` exercises the recorder
and stream checks locally, and runs the installed CLI against a scripted local
API when available. It makes no paid provider calls.
