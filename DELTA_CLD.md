# Claude Messages API compliance TODO

Last review: 2026-09-18 (Anthropic Go SDK v1.71.0)

Open items for `POST /v1/messages` and `POST /v1/messages/count_tokens`
against the Anthropic Messages reference. Each item is a gap; nothing that
already works is listed in the checklist. Priorities: **P0** wrong answer or silent 200,
**P1** gap on an otherwise supported path, **P2** missing feature.

## Recent-release review

Reviewed the [release notes](https://platform.claude.com/docs/en/release-notes/overview)
through September 18 and the linked feature guides, including betas. September
18 only adds Compliance API coverage and does not change the inference adapter.

Every inference request follows wire format → `pkg/provider` → target provider.
There is no Claude passthrough or raw-request context hook. No fields were added
to the shared completion, compaction, or reasoning types for this work.

Implemented and tested:

- On-demand and threshold compaction retain the existing trigger/threshold
  abstraction and signature scoping.
- `tools[].strict` maps to the existing `provider.Tool.Strict`. Each backend
  uses its existing schema support (Gemini uses validated function calling;
  Bedrock support depends on the model).
- Per-message effort uses `ConfigurationUpdate.ReasoningEffort`. Supported
  Claude models and OpenAI Responses retain positional updates. Other Claude
  models, Chat Completions, Gemini, Bedrock, and xAI resolve the latest update
  into the next request's effort without changing the caller's history.
  This fallback does not preserve positional caching guarantees.
- Backend thinking and signatures are returned for model defaults as well as
  explicit thinking requests, over HTTP and SSE. Signature-only blocks include
  an empty `thinking` string. Reported thinking token counts are preserved.
- Unknown fields, trailing JSON, missing model/messages, unsupported enum
  values, and forced tool choice on Fable/Mythos 5.1 return explicit errors.

Unsupported controls now fail rather than disappear: `clear_at`, tool changes,
thinking display `updates`, binding controls, task budgets, explicit cache
markers/TTL, and non-compaction context edits. `top_p`, `top_k`, and `metadata`
were removed from the advertised interface. Computer/browser toolsets remain
explicitly unsupported. Hosted tool search on Fable 5.1 is rejected until its
results can be replayed without changing the signed prefix; client tool search
remains available through the shared tool-search representation.

Claude Files/Skills, hosted code execution, advisor, and MCP connector support
would be separate feature work. Managed Agents and administrative endpoints are
outside the current inference adapter's scope. The separate Anthropic research
provider already uses hosted web search and code execution; that does not expose
those tools through `POST /v1/messages`.

## Request validation

- [ ] P1 Validate `budget_tokens` and sampling ranges.
- [ ] P1 Validate mid-conversation `role: "system"` placement and model support.
- [ ] P2 Support turn-scoped instructions and tool additions/removals through
      shared conversation state. These controls are currently rejected.
      See [mid-conversation system messages](https://platform.claude.com/docs/en/build-with-claude/mid-conversation-system-messages).
- [ ] P1 Confirm or reject `max_tokens: 0` per backend (forwarded verbatim).
- [ ] P2 Return an Anthropic error body on authentication failure (bare 401
      today).

## Request fields

- [ ] P2 Support currently rejected fields where a shared concept is useful:
      top-level `cache_control`, `fallbacks`,
      `fallback_credit_token`, `container`, `inference_geo`, `speed`,
      `diagnostics`, `mcp_servers`, `service_tier`,
      `output_config.task_budget`.
      [Task budgets](https://platform.claude.com/docs/en/build-with-claude/task-budgets)
      are advisory across an agentic loop; they are not equivalent to the
      existing per-request `MaxTokens`.
- [ ] P2 Validate `anthropic-beta` and `anthropic-version`; honor
      `anthropic-user-profile-id`.

## Thinking

- [ ] P2 Support progress updates through shared commentary events; the
      `thinking.display: "updates"` control is currently rejected.
- [ ] P1 Support `thinking.block_binding` and report `input_transformations`;
      the explicit control is currently rejected.
      [Prefix binding](https://platform.claude.com/docs/en/build-with-claude/preserved-thinking)
      is enforced by default for Fable 5.1 on newer accounts. Also verify the
      hosted tool-search replay: `defer_loading` changes after a tool is used
      because hosted search result blocks are missing from shared history.
      Fable 5.1 hosted tool search is blocked until the full replay path exists.
- [ ] P1 Preserve a fixed `budget_tokens` for Claude backends instead of a
      coarse effort (Claude 4.5 and older receive no thinking at all).

## Cache control

- [ ] P2 Design shared cache policy if explicit placement/TTL is needed.
      Explicit markers are rejected today; the Claude adapter's automatic
      top-level caching remains enabled.

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

- [ ] P2 Add `eager_input_streaming`, `allowed_callers`, `cache_control`,
      `input_examples`.
- [ ] P2 Support [computer](https://platform.claude.com/docs/en/agents-and-tools/tool-use/computer-use-tool)
      and [browser](https://platform.claude.com/docs/en/agents-and-tools/tool-use/browser-use-tool)
      `*_toolset_20260801` (rejected today). SDK v1.71.0 already has both types;
      support requires changes to the common conversion path. It must include
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

- [ ] Turn-scoped instruction expiry and thinking-display/binding controls.
- [ ] Fable 5.1 signed-thinking replay with tool search and binding enforcement.
- [ ] `max_tokens: 0` across backends.
- [ ] Wire-shape fixtures (JSON and SSE) for every stop reason and for
      envelope / usage field presence.

## Proposed shared design for remaining features

Keep provider API structs at the server/provider boundaries. Add common fields
only for semantics every adapter can either implement or explicitly reject.

1. **Conversation state:** extend the existing configuration update concept
   with instruction lifetime and active tool names. A common resolver computes
   current instructions/tools for adapters without positional controls; capable
   adapters retain the original events. Expiry must distinguish ordinary user
   turns from tool-result continuations. Stable signed prefixes require complete
   replay or deliberate removal of incompatible reasoning state; merely removing
   expired text from old messages is insufficient.
2. **Tool collections:** reuse `Tool.Tools`, namespaces, and normal tool
   calls/results. Define computer/browser member schemas and a portable browser
   state result. Providers with built-in toolsets can compile these to native
   definitions; others use function tools with the same member identities and
   executor. Preserve ordered execution and stop-on-error behavior.
3. **Hosted tool replay and progress:** carry hosted calls/results through the
   existing `ToolCall`/`ToolResult` representation, and progress through
   `MessagePhaseCommentary`. Both API frontends must round-trip these items before
   enabling prefix-bound search or provider progress controls. No raw provider
   request envelopes are needed.
4. **Budgets, cache, and binding:** put a total task budget in shared agent-loop
   accounting, separate from per-response `MaxTokens`. Treat cache policy and
   invalid reasoning state as capability-aware adapter policies. Report any
   dropped state through common diagnostics if that becomes a product requirement.

These are proposals, not implemented support. Scoped instructions, toolsets,
progress/binding controls, and total task budgets remain explicit errors.

## Review verification

`server/anthropic/features_e2e_test.go` exercises the real API handlers, shared
representation, signature adapter, and SDK request/response conversion with
mocked upstream transports. Its matrix covers both Messages and Responses with
Claude (positional and fallback), OpenAI Responses and Chat, Gemini, xAI, and
Bedrock. It checks current effort, accompanying instructions, strict-tool
conversion, default-thinking HTTP/SSE replay, and explicit unsupported errors.

`test/anthropic/features/release_e2e_test.go` passed against the real Claude API
on September 18: default-thinking HTTP and SSE, signed second-turn replay, and
strict-tool execution with effort changes before and after the tool call.
Run it with `CLAUDE_RELEASE_LIVE=1`.

Compaction and mid-conversation instruction live coverage remains in
`test/anthropic/features/context_e2e_test.go`, gated by `CLAUDE_CONTEXT_LIVE=1`.
