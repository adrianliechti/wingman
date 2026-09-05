# Claude Messages API compliance TODO

Last review: 2026-09-05 (`1313d4002804`, working tree)

Open items for `POST /v1/messages` and `POST /v1/messages/count_tokens`
against the Anthropic Messages reference. Each item is a gap; nothing that
already works is listed. Priorities: **P0** wrong answer or silent 200,
**P1** gap on an otherwise supported path, **P2** missing feature.

## Request validation

- [ ] P0 Reject a missing `model` and a missing or empty `messages`.
- [ ] P0 Reject unknown top-level fields and trailing JSON.
- [ ] P1 Validate `tool_choice.type`, `tool_choice.name` for `type: "tool"`,
      `thinking.type` and `thinking.display` values, `budget_tokens` range,
      sampling ranges, `metadata.user_id`.
- [ ] P1 Reject `role: "system"` inside `messages`.
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
- [ ] P2 Validate `anthropic-beta` and `anthropic-version`; honor
      `anthropic-user-profile-id`.

## Thinking

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

- [ ] P1 Support `compact_*` `instructions` and `pause_after_compaction`.
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

- [ ] P1 Add `strict` on custom tools.
- [ ] P2 Add `eager_input_streaming`, `allowed_callers`, `cache_control`,
      `input_examples`.
- [ ] P2 Support `computer_toolset_20260801` and `browser_toolset_20260801`
      once the SDK carries them (rejected today); support or keep rejecting
      code execution, memory, web search / fetch, advisor, MCP toolsets.
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

- [ ] Required-field and `max_tokens: 0` end-to-end cases.
- [ ] Wire-shape fixtures (JSON and SSE) for every stop reason and for
      envelope / usage field presence.
- [ ] Explicit rejection tests for unsupported blocks, tools, and top-level
      fields.
