# Model routing

Wingman has two routing layers:

- `roundrobin` and `adaptive` distribute equivalent models across providers and fail over on provider health.
- `classifier` and `stage` choose a model for each request. They are opt-in and are registered after load-balancing routers, so a decision candidate may itself be a router.

All router types share one streaming failover contract. A candidate is not committed until it emits meaningful message content. Role-only stream preludes remain buffered, so Wingman can safely try the next candidate after a provider error, an empty stream, or `first_token_timeout`. Caller cancellation and non-provider 4xx request errors are not retried.

For a complete cross-vendor setup using the current GPT-5.6 and Claude families, see [`examples/coding-router.yaml`](../examples/coding-router.yaml). It exposes both the `coding` classifier and `coding-stage` policy as model IDs.

## Classifier router

The classifier applies hard constraints first, then a local difficulty estimate. An embedder and an LLM judge are optional escalation tiers for ambiguous requests. With both omitted, classification is local and adds no model call.

```yaml
routers:
  coding:
    type: classifier
    default: capable
    session_affinity: true
    first_token_timeout: 30s

    candidates:
      - model: efficient
        card: Fast model for routine edits and questions
        cost: 1
        max_difficulty: 2
        max_context: 128000
        examples:
          - Rename a symbol and update its tests

      - model: capable
        card: Capable model for debugging and architecture work
        cost: 4
        max_difficulty: 4
        vision: true
        max_context: 200000
        examples:
          - Diagnose a cross-package concurrency failure

    # embedder: embedding-model
    # margin: 0.05
    # completer: judge-model
    # message_hash_fallback: false
```

`session_affinity` reuses a decision only when the request supplies a supported session identity. Subagents with their own agent/thread identity receive an independent affinity key. `message_hash_fallback` is off by default because identical prompts from unrelated callers must not accidentally share decisions.

### Offline replay

`router-replay` evaluates classifier configuration without calling a routed candidate. Optional embedder/judge tiers are not configured by the CLI, so it evaluates the local classifier tier.

Candidate file:

```json
[
  {"model":"efficient","card":"routine work","cost":1,"max_difficulty":2,"max_context":128000},
  {"model":"capable","card":"complex work","cost":4,"max_difficulty":4,"vision":true,"max_context":200000}
]
```

Replay input is JSONL. `ExpectedModel` is optional:

```json
{"ID":"case-1","Messages":[{"Role":"user","Content":[{"Text":"Rename this helper and update its tests"}]}],"ExpectedModel":"efficient"}
{"ID":"case-2","Messages":[{"Role":"user","Content":[{"Text":"Diagnose a distributed deadlock across these services"}]}],"ExpectedModel":"capable"}
```

```sh
go run ./tools/router-replay -candidates candidates.json -input cases.jsonl -default capable -details
```

The report includes accuracy, a confusion matrix, under/over-routing counts, selection sources, and mean configured cost delta.

## Stage router

The stage router is a small, explainable two-tier policy for coding-agent loops. It favors the capable model while exploring, recovering from errors, or after compaction, and the efficient model for settled write/edit work and clean test passes.

```yaml
routers:
  coding-stage:
    type: stage
    capable: capable
    efficient: efficient
    picker: efficient_first
    confidence_threshold: 0.5
    recent_turn_window: 3
    first_token_timeout: 30s
```

It routes each turn independently rather than pinning a whole session. The selected tier still fails over to the other tier under the shared streaming contract.

## Coding-agent request metadata

Normalized metadata lives in `pkg/request`; transport middleware attaches it to the request context. `x-wingman-*` values are the explicit Wingman override contract and take precedence over native client headers.

| Client | Extracted request headers | Evidence |
| --- | --- | --- |
| Claude Code | `x-claude-code-session-id`, `x-claude-code-agent-id`, `x-claude-code-parent-agent-id`, `x-client-request-id`, `User-Agent` | The three lineage headers are in Anthropic's [LLM gateway protocol](https://code.claude.com/docs/en/llm-gateway-protocol). Session ID, correlation ID, and the `claude-cli/<version>` user agent are also present in the local Claude Code source. |
| Codex | `session-id`, `thread-id`, `x-codex-parent-thread-id`, `x-openai-subagent`, `x-codex-turn-metadata`, `originator`, `x-client-request-id`, `User-Agent` | Source-verified at OpenAI Codex commit [`5186e2c`](https://github.com/openai/codex/tree/5186e2ccc305ab54a2bb1239fda3656d8b4ab950), notably [session headers](https://github.com/openai/codex/blob/5186e2ccc305ab54a2bb1239fda3656d8b4ab950/codex-rs/codex-api/src/requests/headers.rs), [turn metadata](https://github.com/openai/codex/blob/5186e2ccc305ab54a2bb1239fda3656d8b4ab950/codex-rs/core/src/responses_metadata.rs), and [client identity](https://github.com/openai/codex/blob/5186e2ccc305ab54a2bb1239fda3656d8b4ab950/codex-rs/login/src/auth/default_client.rs). These are compatibility headers, not a documented OpenAI API contract. |
| OpenCode | `x-opencode-session`, `x-opencode-request`, `x-opencode-client`, `x-session-affinity`, `X-Session-Id`, `x-parent-session-id`, `User-Agent` | Source-verified in OpenCode's [request assembly](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/session/llm/request.ts). The hosted-provider and custom-provider paths send different session header sets. |
| VS Code / GitHub Copilot | `X-Request-Id`, `X-Interaction-Type`, `X-Initiator`, `Copilot-Integration-Id`, `User-Agent` | Source-verified at VS Code commit [`8715d62`](https://github.com/microsoft/vscode/tree/8715d6263b77e500f4bd360bc3304f9d0fe77756), notably the [model request path](https://github.com/microsoft/vscode/blob/8715d6263b77e500f4bd360bc3304f9d0fe77756/extensions/copilot/src/platform/networking/common/networking.ts) and [user-agent injection](https://github.com/microsoft/vscode/blob/8715d6263b77e500f4bd360bc3304f9d0fe77756/extensions/copilot/src/platform/networking/node/nodeFetcher.ts). Its chat session IDs are trace attributes, not model-request headers, so Wingman does not infer Copilot session affinity. |
| OpenAI Agents JS | `User-Agent` | Source-verified at [`0e384ce`](https://github.com/openai/openai-agents-js/blob/0e384ceed488647a97dd3814dadc99b763c14a4d/packages/agents-openai/src/defaults.ts): `Agents/JavaScript <version>`. The SDK does not add agent/session HTTP headers. |

Wingman deliberately ignores Codex installation IDs, routing hints, and turn-state headers: they are device or internal transport data, not conversation identity. `User-Agent` identifies a client implementation only and is never used as a user, session, or affinity key. Header and JSON metadata values are bounded before entering context or telemetry.

## Telemetry

Routers add trace events named `wingman.router.decision`, `wingman.router.attempt`, and `wingman.router.fallback`, plus these OpenTelemetry metrics:

- `wingman.router.decisions`
- `wingman.router.attempts`
- `wingman.router.fallbacks`
- `wingman.router.decision.score`
- `wingman.router.time_to_first_content`

Session, agent, client, originator, and request-kind metadata is also attached to inference spans when present.
