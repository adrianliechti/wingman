# Claude Messages API compliance TODO

Last review: 2026-09-17 (`2835afd9`, Anthropic Go SDK v1.71.0)

Open items for `POST /v1/messages` and `POST /v1/messages/count_tokens`
against the Anthropic Messages reference. Each item is a gap; nothing that
already works is listed in the checklist. Priorities: **P0** wrong answer or silent 200,
**P1** gap on an otherwise supported path, **P2** missing feature.

## Recent-release review

Reviewed the [release notes](https://platform.claude.com/docs/en/release-notes/overview)
through September 17 and the linked feature guides, including betas. Core
support covers on-demand and threshold compaction, top-level effort, explicitly
requested adaptive thinking, structured output, ordinary tool calls, and text
mid-conversation system messages on supported Claude models. These do not imply
complete native Messages API compatibility.

The first fixes should preserve default thinking responses and wire up strict
tools and per-message effort. The latter two already have common representations:
`provider.Tool.Strict` and `provider.ConfigurationUpdate.ReasoningEffort`.
Unsupported controls should receive explicit errors until their semantics can
be preserved; adding Claude-specific fields to every provider is unnecessary
for these fixes.

Claude Files/Skills, hosted code execution, advisor, and MCP connector support
would be separate feature work. Managed Agents and administrative endpoints are
outside the current inference adapter's scope. The separate Anthropic research
provider already uses hosted web search and code execution; that does not expose
those tools through `POST /v1/messages`.

## Request validation

- [ ] P0 Reject a missing `model` and a missing or empty `messages`.
- [ ] P0 Reject unknown top-level fields and trailing JSON.
- [ ] P1 Validate `tool_choice.type`, `tool_choice.name` for `type: "tool"`,
      `thinking.type` and `thinking.display` values, `budget_tokens` range,
      sampling ranges, `metadata.user_id`.
- [ ] P1 Validate mid-conversation `role: "system"` placement and model support.
- [ ] P0 Preserve or reject turn-scoped `clear_at`: currently stripped, making
      a temporary instruction persist on later turns. Tool additions/removals
      are rejected. See [mid-conversation system messages and tool changes](https://platform.claude.com/docs/en/build-with-claude/mid-conversation-system-messages).
- [ ] P0 Map per-message `output_config.effort` to `ConfigurationUpdate` and
      serialize it in the Claude provider with the appropriate beta. Currently
      an effort-only system message disappears, retaining the old top-level
      effort. See [per-message effort](https://platform.claude.com/docs/en/build-with-claude/effort#per-message-effort-beta).
- [ ] P0 Reject unsupported forced tool choice on Fable/Mythos 5.1 instead of
      silently changing it to automatic selection. See [model migration changes](https://platform.claude.com/docs/en/models/fable-5-1/whats-new-fable-5-1).
- [ ] P1 Confirm or reject `max_tokens: 0` per backend (forwarded verbatim).
- [ ] P2 Return an Anthropic error body on authentication failure (bare 401
      today).

## Request fields

- [ ] P1 Reject `top_p` and `top_k` instead of ignoring them (deprecated
      upstream); remove them and `metadata` from `API.md`.
- [ ] P1 Forward `metadata.user_id` or reject it.
- [ ] P2 Honor or reject: top-level `cache_control`, `fallbacks`,
      `fallback_credit_token`, `container`, `inference_geo`, `speed`,
      `diagnostics`, `mcp_servers`, `service_tier`,
      `output_config.task_budget`.
      [Task budgets](https://platform.claude.com/docs/en/build-with-claude/task-budgets)
      are advisory across an agentic loop; they are not equivalent to the
      existing per-request `MaxTokens`. Local probes confirm they are discarded.
- [ ] P2 Validate `anthropic-beta` and `anthropic-version`; honor
      `anthropic-user-profile-id`.

## Thinking

- [ ] P0 Preserve upstream thinking blocks/signatures when thinking is enabled
      by the model's default. `thinkingEnabled(options)` currently hides them
      in both HTTP and SSE unless the client explicitly requests thinking or
      effort; clients cannot replay the omitted reasoning on the next turn.
      See [thinking defaults and preservation](https://platform.claude.com/docs/en/build-with-claude/thinking).
- [ ] P0 Preserve or reject `thinking.display: "updates"`: currently converted
      to `"summarized"`, with no progress-update beta header.
- [ ] P1 Support or explicitly reject `thinking.block_binding` and report
      `input_transformations`; both the control and its beta are currently lost.
      [Prefix binding](https://platform.claude.com/docs/en/build-with-claude/preserved-thinking)
      is enforced by default for Fable 5.1 on newer accounts. Also verify the
      provider's tool-search replay: `defer_loading` changes after a tool is
      used, editing the prefix. This mutation is reproduced locally; its effect
      on signed thinking still needs a live test with binding enabled.
- [ ] P1 Preserve a fixed `budget_tokens` for Claude backends instead of a
      coarse effort (Claude 4.5 and older receive no thinking at all).
- [ ] P1 Return `usage.output_tokens_details` whenever the backend reports
      thinking tokens, matching the reference.

## Cache control

- [ ] P1 Carry `system[].cache_control`, per-block and tool `cache_control`,
      and `ttl` to the Anthropic backend instead of the fixed top-level
      marker.
- [ ] P1 Reject cache placement on backends that cannot honor it.

## Context management

- [ ] P1 Support `compact_*` `instructions` and `pause_after_compaction`
      (explicitly rejected; common trigger/threshold compaction is supported).
- [ ] P1 Report applied edits (`context_management` in the response and
      `message_delta`).
- [ ] P2 Support `clear_tool_uses_*` and `clear_thinking_*` edits.

## Input content

- [ ] P1 Round-trip `server_tool_use`, `web_search_tool_result`,
      `web_fetch_tool_result` natively instead of as marker text.
- [ ] P2 Accept `search_result`, `mcp_tool_use`, `mcp_tool_result`,
      `container_upload`, `mid_conv_system`, `fallback`, `tool_addition`,
      `tool_removal`, advisor / code-execution / tool-search result blocks,
      and `source.type: "file"` / `"content"` (all rejected with a field
      path today).
- [ ] P2 Support document `context`, `title`, `citations`; text `citations`;
      image transformations.

## Tools

- [ ] P0 Forward custom-tool `strict` from the Messages endpoint. The Claude
      provider already supports `Tool.Strict`, but `ToolParam` and `toTools`
      discard the incoming flag. See [strict tool use](https://platform.claude.com/docs/en/agents-and-tools/tool-use/strict-tool-use).
- [ ] P2 Add `eager_input_streaming`, `allowed_callers`, `cache_control`,
      `input_examples`.
- [ ] P2 Support [computer](https://platform.claude.com/docs/en/agents-and-tools/tool-use/computer-use-tool)
      and [browser](https://platform.claude.com/docs/en/agents-and-tools/tool-use/browser-use-tool)
      `*_toolset_20260801` (rejected today). SDK v1.71.0 already has both types;
      the SDK limitation comment in `toTools` is stale. Support must include
      member configuration, `toolset_name` on calls/results, and browser-state
      results, not just accepting the tool definition. Legacy computer use is
      still supported upstream, so this is an optional migration.
- [ ] P2 Support or keep rejecting code execution, memory, web search / fetch,
      advisor, MCP toolsets.
- [ ] P2 Honor variant-specific options on built-in tools.

## Response object

- [ ] P1 Emit `container`, `context_management`, `diagnostics` (null when
      unused).
- [ ] P1 Emit `usage.cache_creation` breakdown, `iterations`,
      `server_tool_use`, `service_tier`, `inference_geo`, `speed`,
      `fallback_credit`.
- [ ] P1 Emit `stop_details.fallback_credit_token`,
      `fallback_has_prefill_claim`, `recommended_model`.
- [ ] P1 Emit `citations` on text blocks (empty array when none).
- [ ] P2 Emit the server / MCP / container / fallback result blocks
      (`web_search_tool_result`, `web_fetch_tool_result`,
      `advisor_tool_result`, `code_execution_tool_result`,
      `bash_code_execution_tool_result`,
      `text_editor_code_execution_tool_result`, `tool_search_tool_result`,
      `mcp_tool_use`, `mcp_tool_result`, `container_upload`, `fallback`).
- [ ] P2 Emit assistant-generated files.
- [ ] P2 Add `request-id` and workspace response headers.

## Stop sequences

- [ ] P1 Report the matched `stop_sequence` value on Bedrock (Converse does
      not return it).
- [ ] P1 Report `stop_reason: "stop_sequence"` for Gemini backends (Gemini
      finishes with `STOP`; the sequence is applied but not signalled).

## Streaming

- [ ] P1 Emit `citations_delta`, `thinking_delta.estimated_tokens`,
      `message_delta.context_management`, `message_delta.delta.container`.
- [ ] P1 Report `input_tokens` in `message_start` for non-Anthropic backends
      (currently `0` until `message_delta`).
- [ ] P2 Emit `ping`.

## count_tokens

- [ ] P2 Use the backend's tokenizer where available instead of the local
      estimate.

## Tests to add

- [ ] Default-thinking HTTP/SSE signature and token-detail preservation.
- [ ] Strict-tool and per-message-effort endpoint-to-provider round trips.
- [ ] Turn-scoped instruction expiry and thinking-display/binding controls.
- [ ] Fable 5.1 signed-thinking replay with tool search and binding enforcement.
- [ ] Required-field and `max_tokens: 0` end-to-end cases.
- [ ] Wire-shape fixtures (JSON and SSE) for every stop reason and for
      envelope / usage field presence.
- [ ] Explicit rejection tests for unsupported blocks, tools, and top-level
      fields.

## Review verification

`go test ./pkg/provider/anthropic ./server/anthropic` passes. Eighteen local
probes exercised the real Messages handler and Claude provider with a mocked
upstream transport. They confirmed the silent drops/conversions above, loss of
default thinking in HTTP/SSE, deferred-tool mutation, and explicit rejection of
tool removal and both new toolsets. These probes verify proxy behavior, not
upstream model acceptance. Existing live compaction and mid-conversation-system
tests remain in `test/anthropic/features/context_e2e_test.go`; they were not
rerun for this review.
