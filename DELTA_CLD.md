# Claude Messages API — TODO

Gaps between Wingman's `POST /v1/messages` + `POST /v1/messages/count_tokens`
and the [Anthropic Messages API](https://platform.claude.com/docs/en/api/messages).
Delete an item once it lands; add new gaps where they belong.

**P0** wrong answer or silent 200 · **P1** broken on a supported path ·
**P2** missing feature. Code paths: `server/anthropic` (wire format) →
`pkg/provider` (shared types) → `pkg/provider/*` (backends).

Out of scope: Files/Skills, Managed Agents, admin endpoints, advisor pairings.

## P1

- [ ] **Computer/browser toolsets.** Accept `computer_toolset_20260801` and
      `browser_toolset_20260801` (rejected today). Fable/Mythos 5.2, Opus 5.5,
      Sonnet 5.5, and Haiku 5.5 reject `computer_20251124` on the Claude API,
      so computer use is broken there; Bedrock still takes the legacy tool.
      - Define member schemas + a portable browser-state result on
        `provider.Tool.Tools`; carry `toolset_name` on calls/results.
      - Claude adapter: send the toolset instead of `computer_20251124`
        (`pkg/provider/anthropic/completer.go`, `computeruse` branch) for
        models that require it; keep legacy for Bedrock.
      - Other backends: compile members to function tools with the same
        names; keep ordered, stop-on-error execution.
- [ ] **`tool_result` content blocks.** Accept `tool_reference` and
      `search_result` inside `tool_result.content`, so client-side tool search
      can load deferred tools (`server/anthropic/convert.go`,
      `toToolResultParts`).
- [ ] **Hosted web search/fetch history.** Round-trip calls and results
      through `ToolCall`/`ToolResult` like hosted tool search; they are
      flattened to text markers today (`convert.go`, `webSearchResultMarker`,
      `webFetchResultMarker`).
- [ ] **Validation.** Reject `thinking.type: "enabled"` without
      `budget_tokens` and out-of-range `budget_tokens`
      (`handler_messages.go`, `validateMessageRequest`).
- [ ] **Mid-conversation `role: "system"`.** Validate placement (after a user
      turn, last or followed by assistant, never first) and reject it for
      models without support instead of folding it into `system`.
- [ ] **`max_tokens: 0` on other backends.** Gemini drops a zero
      `maxOutputTokens` (genai `omitempty`) and returns a full answer; check
      Bedrock and OpenAI. Reject where the backend cannot honor it.
- [ ] **Fixed `budget_tokens`.** Pass the exact budget to Claude backends that
      accept it instead of mapping to an effort level; Claude 4.5 and older
      currently get no thinking at all.
- [ ] **Compaction options.** Support `instructions` and
      `pause_after_compaction` on `compact_*` edits (rejected today;
      `validateCompactionRequest`).
- [ ] **Applied context edits.** Emit `context_management` on the response
      and `message_delta`.
- [ ] **Response envelope.** Emit `context_management` when applicable;
      `stop_details.fallback_credit_token`,
      `fallback_has_prefill_claim`, `recommended_model`; `citations: []` on
      text blocks (`server/anthropic/models.go`, `convert.go`).
- [ ] **Usage fields.** Emit `usage.cache_creation` breakdown, `iterations`,
      `server_tool_use`, `service_tier`, `inference_geo`, `speed`,
      `fallback_credit`.
- [ ] **Stop sequences.** Report the matched `stop_sequence` on Bedrock
      (Converse doesn't return it; match locally) and
      `stop_reason: "stop_sequence"` for Gemini (finishes with `STOP`).
- [ ] **Streaming.** Emit `citations_delta`,
      `thinking_delta.estimated_tokens`, `message_delta.context_management`;
      report `input_tokens` in `message_start` for non-Anthropic backends
      (0 until `message_delta` today).

## P2

- [ ] **Tool additions/removals** (`tool_addition` / `tool_removal`, incl.
      inline definitions from `inline-tools-2026-09-15`). Extend conversation
      updates with the active tool set; resolve it into `Tools` for backends
      without positional updates, like `ResolveConfigurationUpdates`.
- [ ] **`between_tools` elsewhere.** Only Sonnet 5.5 keeps between-tool
      progress notes; other backends treat it as fully disabled.
- [ ] **Context edits.** Support `clear_tool_uses_*` and thinking retention
      counts other than all / one turn.
- [ ] **Explicit cache placement.** The Anthropic frontend never selects
      `CacheModeExplicit`, so markers are ignored and automatic caching stays
      on; only `ttl: "1h"` is honored (`toCacheOptions`).
- [ ] **Request options.** Honor or reject top-level `cache_control`,
      `fallbacks`, `fallback_credit_token`, `container`, `inference_geo`,
      `speed`, `diagnostics`, `mcp_servers`, `service_tier`,
      `output_config.task_budget` (advisory loop budget, not `MaxTokens`).
- [ ] **Headers.** Validate `anthropic-beta` / `anthropic-version`; honor
      `anthropic-user-profile-id`; return `request-id` and workspace headers.
- [ ] **Input blocks** (rejected with a field path today): `search_result`,
      `mcp_tool_use`, `mcp_tool_result`, `container_upload`,
      `mid_conv_system`, `fallback`, advisor / code-execution results,
      `source.type: "file"` / `"content"`.
- [ ] **Documents and images.** Document `context`, `title`, `citations`;
      text `citations`; image transformations.
- [ ] **Tool fields.** `eager_input_streaming`, `allowed_callers`,
      `input_examples`; variant-specific options on built-in tools.
- [ ] **Hosted tools.** Support or keep rejecting code execution, memory,
      web search/fetch, advisor, MCP toolsets.
- [ ] **Output blocks.** `web_search_tool_result`, `web_fetch_tool_result`,
      `advisor_tool_result`, `code_execution_tool_result`,
      `bash_code_execution_tool_result`,
      `text_editor_code_execution_tool_result`, `mcp_tool_use`,
      `mcp_tool_result`, `container_upload`, `fallback`; generated files.
- [ ] **`ping` events** in streams (type exists, never sent).
- [ ] **`count_tokens` accuracy.** Count `thinking`, `tool_choice`,
      `output_config`, `context_management` (dropped by
      `CountTokensRequest`); use the backend tokenizer where available.
- [ ] **Model discovery.** Expose upstream Models API capabilities and limits,
      including `line`, `thinking.types.disabled`, and `server_tools`.
      `server_tools.code_execution` means the server tool is accepted;
      top-level `code_execution` means programmatic tool calling is supported.

## Tests

- [ ] `max_tokens: 0` on non-Claude backends.
- [ ] Fable 5.1/5.2 signed replay with default binding enforcement (5.2
      enforces the prefix check on every account).
- [ ] JSON and SSE wire fixtures for every stop reason and envelope / usage
      field presence.

## Release review — November 10, 2026

Reviewed [Claude Fable 5.2](https://platform.claude.com/docs/en/models/fable-5-2/overview)
(`claude-fable-5-2`, `claude-mythos-5-2`; Bedrock `anthropic.claude-fable-5-2`),
released November 9, against the current adapters.

- **Adopted: Fable/Mythos 5.2.** The `fable-5` / `mythos-5` substring
  patterns already cover always-on adaptive thinking, progress updates,
  positional system messages, Bedrock all-turn retention, and the 2026
  tokenizer. Added the 5.2 identifiers to the forced-tool-choice rejection
  list and the per-message effort allowlist, which matched 5.1 only. Fast
  mode (`speed: "fast"`) needs no beta header on 5.2 and is passed through
  unchanged; the standard-speed fallback covers platforms without it.
- **Breaking on 5.2, tracked above:** the preserved-thinking prefix check
  applies on every account (the Tests item), `computer_20251124` returns 400
  on every platform except Bedrock (the P1 toolsets item), and only 5.2
  models read 5.2 thinking blocks, so a router that moves a conversation to
  Opus 5.5 or Fable 5.1 loses its reasoning. Advisor pairings stay out of
  scope.
- **Operational:** Fable 5.2 costs $8 / $40 per MTok (cache reads $0.20);
  there is no static price table here to update.

## Release review — October 8, 2026

Reviewed the [platform release notes](https://platform.claude.com/docs/en/release-notes/overview)
from September 1 through October 8 against the current adapters.

- **Adopted: Haiku 5.5.** Default-thinking summaries, disabled/forced-tool
  effort caps, newer tokenizer estimates, Bedrock all-turn thinking retention,
  and Claude API positional system/effort updates. The existing adapters
  already select content by type, replay signature-only blocks, omit sampling,
  support adaptive thinking, and propagate refusals. Defaults allow 128K output.
  See the [migration guide](https://platform.claude.com/docs/en/models/haiku-5-5/migration-guide).
- **Next: computer/browser toolsets.** Reached GA in `anthropic-sdk-go` v1.82.0
  (`computer_toolset_20260801`, `browser_toolset_20260801`, `toolset_tool_use`,
  `browser_state`). These close the remaining model migration gaps above.
  Optional thinking binding controls and `input_transformations` diagnostics
  are intentionally omitted. Until then, native computer use through the Claude
  API remains incompatible on the new models. Signed histories must stay
  append-only and use the originating account; Converse lowering of
  instruction/effort updates can change the signed prefix.
- **Next: cache diagnostics and model discovery.** Both reached GA in
  `anthropic-sdk-go` v1.82.0 (`MessageNewParams.Include` / `Message.CacheDiagnostics`
  and `ModelInfo` capabilities). Diagnostics reached GA September 23; support
  its request/response fields without requiring the old beta header. Model
  discovery gained `line` (October 1), disabled-thinking support (October 5),
  and server-tool support (October 6).
- **Later: inline tool definitions.** September 22 added definitions inside
  positional system messages; implement with tool additions/removals above.
  On-demand compaction and progress displays already have adapter support.
- **Operational:** Sonnet 4.5 retires November 30; migrate configured callers
  to Sonnet 5.5. Sonnet 5.5 cache reads became cheaper October 7; there is no
  static price table here to update. Admin/Compliance, Managed Agents network
  changes, and Python/TypeScript automation toolsets do not require changes to
  this Go Messages/Converse gateway.

Verification uses local request and replay fixtures, not paid upstream calls.
Regional Bedrock availability and access still need an account-specific smoke test.
