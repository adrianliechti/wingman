# Claude Messages API compatibility delta

Audit date: 2026-08-30

Repository revision: `616ec9b1ba44`

Previous audit: 2026-07-24 (`06f6bd47cefa`).

Targets:

- `POST /v1/messages` and `POST /v1/messages/count_tokens`, compared with
  Anthropic's
  [Messages reference](https://platform.claude.com/docs/en/api/cli/messages)
  and the beta Messages create contract.
- Platform changes published in the
  [Claude Platform release notes](https://platform.claude.com/docs/en/release-notes/overview)
  since the previous audit.

## Executive summary

Wingman's Anthropic endpoint implements the core Messages flow: synchronous and
streaming text, images and PDFs, multi-turn messages, client tools and tool
results, tool choice, stop sequences, structured JSON output, thinking,
selected computer/bash/text-editor/tool-search tools, basic usage, and a
compaction path.

It is **not wire-compatible with the complete current contract**. The most
important remaining gaps are:

1. `model` and `messages` are still not validated as required at the HTTP
   boundary.
2. Many documented parameters are silently ignored rather than honored or
   rejected.
3. Required/documented response-envelope and usage fields are absent in both
   JSON and SSE responses.
4. The current content-block and tool unions are only partially implemented,
   and unknown blocks are dropped rather than rejected.
5. Cache controls, citations, context management, refusal/fallback data, and
   server-tool/MCP/container state are lost or unsupported.

The silent-ignore behavior remains the highest compatibility risk: a client can
receive HTTP 200 even though Wingman did not apply important requested
semantics.

Three findings from the previous audit have since been fixed and a fourth
partly so; all are recorded as corrections below. The single most consequential *new* gap is
`computer_toolset_20260801`: the August 19 GA computer-use toolset is matched by
Wingman's `computer` prefix and silently downgraded to the legacy single-tool
shape.

## Scope and method

The linked live reference and the release notes were treated as the authority.
Because the reference page is dynamically rendered, the generated types in the
official `github.com/anthropics/anthropic-sdk-go` dependency were also used to
enumerate nested request/response unions. `go.mod` now pins **v1.61.0** (was
v1.58.0); the enumeration below was taken from **v1.67.0**, the newest snapshot
available locally (the release notes reference v1.68.0). The live reference wins
where it differs from the SDK snapshot.

Primary implementation files inspected:

- `server/anthropic/models.go`
- `server/anthropic/handler.go`
- `server/anthropic/handler_messages.go`
- `server/anthropic/handler_tokens.go`
- `server/anthropic/convert.go`
- `server/anthropic/accumulator.go`
- `pkg/provider/completer.go`
- `pkg/provider/anthropic/completer.go`
- `test/anthropic/**`
- `API.md`

This is a compatibility audit, not a requirement that every backend emulate
every Anthropic-only feature. A backend may reject an unsupported capability
with a clear Anthropic-shaped 4xx error. Silently accepting and discarding it is
not compatible behavior.

Status terms:

- **Supported**: represented and materially honored end-to-end.
- **Partial**: accepted but lossy, backend-dependent, or missing documented
  fields.
- **Ignored**: decoded or accepted, but not passed to completion behavior.
- **Missing**: not represented and therefore normally ignored or rejected.

## Changes since the 2026-07-24 audit

### Corrections to the previous audit

Each of these was re-verified in the working tree at this revision.

| Previous claim | Current state |
|---|---|
| CLD-001: `MessageRequest` uses non-pointer fields with no presence-aware validation; `max_tokens: 0` generates normal output | **Partly fixed.** `MaxTokens` is now `*int`, and `validateMessageRequest` rejects a missing `max_tokens`, a negative `max_tokens`, and the documented `max_tokens: 0` conflicts (`stream`, `thinking.type: "enabled"`, `output_config.format`, forced `tool_choice`). `model` and `messages` are still unvalidated — see CLD-001 below. |
| CLD-010: `tool_result.is_error` is parsed but discarded because `provider.ToolResult` has no error flag | **Fixed.** `provider.ToolResult.IsError` exists and `convert.go:154` populates it. |
| CLD-011: built-in tools are accepted by prefix with no rejection of unknown types | **Fixed.** `toTools` now returns a field-path error (`tools.%d: Input tag '%s' ... does not match any of the expected tags`) for unrecognized tool types. |
| CLD-013: `pause_turn`, `compaction`, and `model_context_window_exceeded` stop reasons are missing; `model_context_window_exceeded` is folded into `max_tokens` | **Fixed.** All three constants exist and `convert.go:495-515` maps them from `provider.StopReasonPauseTurn` / `StopReasonCompaction` / `StopReasonContextExceeded`. |
| SDK pinned at `v1.58.0` | `go.mod` pins `v1.61.0`. |

Everything else in the previous audit was re-verified and still holds.

### New API surface Wingman does not cover

Derived from the release notes and a type-level diff of SDK v1.58.0 -> v1.67.0.

| Change | Date | Wingman state |
|---|---|---|
| **`computer_toolset_20260801`** — computer use out of beta as a *toolset*: batch actions, `zoom` on by default, per-member `configs`, responses tagged with `toolset_name` | Aug 19 | **Silently downgraded.** `strings.HasPrefix(t.Type, "computer")` matches the new toolset and maps it to the legacy single computer tool, discarding `configs` and the batch-action contract. A client gets HTTP 200 and the old behavior. |
| **`browser_toolset_20260801`** — new client toolset (accessibility tree, element refs, form input, tab management, downloads, opt-in file upload) | Aug 19 | **Rejected** by `toTools`' default branch. Correct-by-accident: the error message enumerates expected tags and does not mention browser toolsets. |
| Mid-conversation tool changes: `tool_addition` / `tool_removal` content blocks, `BetaToolChangeToolReferenceParam`, MCP tool/toolset references; beta `mid-conversation-tool-changes-2026-07-01` | Jul 24 | **Missing.** Neither block type is in the input union, so both are silently dropped. |
| `fallbacks` gains a `"default"` mode (beta `server-side-fallback-2026-07-01`); `fallback_credit_token` becomes a union with `mode` / `token` / `remove_to_redeem`; usage gains `fallback_credit` with redeemed / not-applied status | Jul 24 | **Missing.** Neither `fallbacks` nor `fallback_credit_token` is modeled; the `fallback` content block is absent from both unions. |
| Files API out of beta — no `files-api-2025-04-14` header; `expires_in_seconds` / `expires_at`; `page`/`next_page` pagination | Aug 19 | Not applicable (Wingman routes no `/v1/files` endpoints), but it removes the beta-header excuse for not supporting `source.type: "file"` on image/document blocks — still missing. |
| Agent Skills / Skills API out of beta; `container` loads skills without a beta header; `BetaSkill` renamed `BetaContainerSkill` | Aug 19, Aug 27 | **Missing.** `container` is not modeled on the request or the response. |
| `anthropic-workspace-id` response header (`wrkspc_`-prefixed) | Aug 11 | **Missing.** Wingman emits no Anthropic-specific response headers. |
| `temperature`, `top_p`, `top_k` **deprecated** on the Messages reference — `temperature` accepts only 1.0, `top_p` only >= 0.99, `top_k` rejected on newer models; Python SDK v1.0 removed all three | Aug 20 | Wingman still forwards `temperature` verbatim to the backend and ignores `top_p`/`top_k`. Ignoring is now closer to upstream than honoring, but neither is validated or rejected, and `API.md` still advertises all three. |
| `stop_details.category` gains `general_harms` | — | Wingman passes the category through as an opaque string, so no change is required; there is no enum validation either way. |
| New web tool variants `web_search_20260318`, `web_fetch_20260318`, `web_fetch_20260309`; `advisor_20260301`; `code_execution_20260521` | — | All rejected by `toTools`, consistent with CLD-011. |
| `BetaImageTransformationsParam` / oversized-image handling | — | **Missing** from the image block. |
| Claude Opus 5 released; `thinking: {"type": "disabled"}` with effort `xhigh`/`max` returns 400; fast mode removed for Opus 4.7; Opus 4.1 retired | Jul 24, Aug 5 | Model-level constraints Wingman does not enforce at its boundary — consistent with CLD-008's under-validation finding. |

## Current coverage

| Area | Status | Notes |
|---|---|---|
| `model`, `messages`, `max_tokens` | Partial | `max_tokens` presence/range/conflict validation now matches the reference; `model` and `messages` are still unchecked. |
| Synchronous and SSE responses | Partial | Core event sequence works; current beta envelope, content, and usage shapes do not. |
| Text and multi-turn messages | Supported | Includes assistant history/prefill paths, subject to backend restrictions. |
| Image base64/URL and PDF base64/URL | Partial | Common forms work; Files API sources and document metadata/citations do not. |
| System prompt | Partial | Text is flattened; block cache controls/citations are discarded. |
| Client tools and tool results | Partial | Basic fields and `is_error` work; `strict`, caching, callers, examples, `eager_input_streaming`, and `allowed_callers` are lost. |
| `tool_choice` | Partial | Known modes map; invalid/underspecified choices are not consistently rejected. |
| Structured output | Supported/partial | `output_config.format` and deprecated `output_format` map to the common schema. Exact validation behavior varies by backend. |
| Thinking | Partial | Adaptive/display works; fixed `budget_tokens` is converted to a coarse effort rather than preserved. |
| Context management | Partial | Only compaction threshold is represented. |
| Prompt caching | Partial | Anthropic upstream is auto-cached, but request-defined placement/TTL is not honored. |
| Computer, bash, text editor, tool search | Partial | Matched by prefix and normalized; variant-specific options are lost, and `computer_toolset_20260801` is silently downgraded to the legacy shape. |
| Stop reasons | Supported | All eight documented values are defined and mapped, including `pause_turn`, `compaction`, and `model_context_window_exceeded`. |
| Browser toolset, mid-conversation tool changes, fallbacks | Missing | Rejected or dropped; none of the Jul-Aug 2026 additions are represented. |
| MCP, code execution, memory, web tools, advisor, containers/skills | Missing | The public Messages surface cannot represent these current beta features. |

## P0: contract correctness

### CLD-001 — Required fields and `max_tokens` semantics

Official request requirements:

- `model` is required.
- `messages` is required.
- `max_tokens` is required and may be `0` for prompt-cache prewarming.

Current behavior — **partly fixed since the previous audit**:

- `MaxTokens` is now `*int`, and `validateMessageRequest`
  (`handler_messages.go:207`) rejects a missing `max_tokens` and a negative
  `max_tokens`.
- The documented `max_tokens: 0` conflicts are rejected: `stream`,
  `thinking.type: "enabled"`, `output_config.format`, and a forced
  `tool_choice` (`any` / `tool`).
- Still unfixed: `model` has no presence check, so a missing `model` resolves
  to the default completer registered under the empty model key.
- Still unfixed: a missing or empty `messages` array is not rejected at the
  HTTP boundary.
- `max_tokens: 0` is now forwarded to the backend as `MaxTokens = 0`. That is
  correct for an Anthropic upstream, which treats it as prompt-cache prewarm,
  but no other backend is guaranteed to interpret `0` that way, and Wingman
  does not check.

Required change:

- Reject missing `model` and missing/empty `messages` with
  `invalid_request_error` alongside the existing `max_tokens` checks.
- Confirm or emulate `max_tokens: 0` prewarm semantics per backend, or reject
  it for backends that cannot honor it. Do not let it generate normal output.

### CLD-002 — Unknown and unsupported input is silently discarded

`json.Decoder` is used without unknown-field or trailing-payload validation.
Several unknown top-level fields therefore disappear. Unknown content-block
types also fall through `toMessage` without an error.

Examples:

- `fallbacks`, `mcp_servers`, `speed`, or `service_tier` can be accepted and
  ignored.
- A `search_result`, `mcp_tool_result`, `container_upload`,
  `mid_conv_system`, `fallback`, `tool_addition`, or `tool_removal` input block
  can be dropped from the prompt. `toMessage` has no default branch, so an
  unrecognized `type` simply produces no content.
- An image or document `source.type` of `file` or `content` falls through
  `toFile`'s switch and yields an empty file rather than an error.
- An unknown `thinking.type`, effort, output format, or tool choice can fall
  back to unrelated/default behavior.

Required change:

- Validate discriminated unions and documented constraints explicitly.
- For a documented but unsupported capability, return a clear Anthropic-shaped
  4xx error naming the field or block.
- For an unknown field, choose and document a compatibility policy. Strict
  rejection is safest for semantics-bearing fields; deliberate pass-through is
  acceptable only when the downstream provider actually receives the value.
- Ensure the request body contains exactly one JSON value.

### CLD-003 — Missing response envelope fields

The current beta response model includes additional envelope state. Wingman's
`Message` does not expose:

| Response field | Wingman | Impact |
|---|---|---|
| `container` | Missing | Container/code-execution state cannot be returned or reused. |
| `context_management` | Missing | Applied context edits are not reported. |
| `diagnostics` | Missing | Opted-in cache-miss diagnostics cannot be returned. |

The same divergence appears in streaming:

- `message_start.message` lacks these documented message fields.
- `message_delta` lacks top-level `context_management`.
- `message_delta.delta` lacks `container`.

Required change:

- Add the documented keys with correct object/null behavior in non-streaming
  and SSE shapes.
- Preserve backend-returned values where supported.
- Where a feature was not requested or cannot apply, emit the documented null
  or empty form rather than silently changing the schema.

### CLD-004 — Required response usage fields are absent

Wingman currently returns basic input/output/cache counts and optional thinking
tokens. It does not return the full current usage contract:

| Usage field | Non-streaming | `message_delta.usage` |
|---|---:|---:|
| `cache_creation` TTL breakdown | Type exists but is never populated | Missing |
| `iterations` | Missing | Missing |
| `server_tool_use` | Missing | Missing |
| `service_tier` | Type exists but is never populated | Not part of delta usage |
| `inference_geo` | Type exists but is never populated | Not part of delta usage |
| `speed` | Missing | Not part of delta usage |
| `output_tokens_details` | Partial | Partial |

`iterations` is especially relevant to compaction, advisor calls, server-side
fallbacks, and multiple sampling loops. The common `provider.Usage` type has no
place to retain it.

Required change:

- Extend the common usage model or add an Anthropic-native side channel that
  preserves these fields.
- Emit documented empty/zero/null values when applicable.
- Add sync and SSE golden tests that assert field presence, not only selected
  token totals.

### CLD-005 — Text citations and streaming citation deltas are missing

Documented text response blocks carry `citations` (an empty array when none are
present). Wingman's `ContentBlock` has no citations field, so ordinary text
blocks already differ in shape.

The SSE delta union also lacks:

- `citations_delta`
- its citation-location union
- `thinking_delta.estimated_tokens` when the corresponding beta is enabled

Required change:

- Model all citation location variants and preserve them through the provider
  layer.
- Include `citations` in text block starts and final text blocks.
- Add `citations_delta` and beta-gated `estimated_tokens` streaming support.

## P1: missing request semantics

### CLD-006 — Top-level request fields missing or ignored

| Official JSON/header field | Current state | Required implementation behavior |
|---|---|---|
| `fallback_credit_token` | Missing | Preserve/reforward credit-token retry semantics or reject. |
| `fallbacks` (CLI `--fallback`) | Missing | Support ordered server-side refusal fallback attempts and the `"default"` mode (beta `server-side-fallback-2026-07-01`), or reject. |
| `container` | Missing | Support ID reuse and `{id, skills}` form or reject. |
| `inference_geo` | Missing | Honor routing constraint and report actual geo, or reject. |
| `speed` (`standard`/`fast`) | Missing | Honor and report selected speed, or reject. |
| top-level `cache_control` | Missing | Apply the automatic last-cacheable-block marker and TTL. |
| `diagnostics.previous_message_id` | Missing | Return cache divergence diagnostics or reject. |
| `mcp_servers` | Missing | Support connector server definitions and MCP toolsets or reject. |
| `service_tier` | Missing | Honor `auto`/`standard_only` and report actual tier, or reject. |
| `output_config.task_budget` | Missing | Preserve `{type:"tokens", total, remaining}` semantics or reject. |
| `metadata.user_id` | **Ignored** | It is decoded but never reaches the provider or abuse-tracking layer. |
| `top_p` | **Ignored** | Decoded but never added to `CompleteOptions`. Now deprecated upstream (only >= 0.99 accepted) — reject it rather than accept-and-drop. |
| `top_k` | **Ignored** | Decoded but never added to `CompleteOptions`. Now deprecated upstream and rejected on newer models. |
| `temperature` | Supported | Forwarded verbatim. Now deprecated upstream (only 1.0 accepted on current models); Wingman applies no range check. |
| `container` (skills) | Missing | Skills are out of beta as of Aug 19; `container` cannot be sent or returned. |
| `anthropic-user-profile-id` header | Missing | Attribute the request when the required beta is enabled, or reject. |
| `anthropic-beta` header | Ignored | No beta validation, gating, or behavior selection. |
| `anthropic-version` header | Ignored | No version validation or compatibility selection. |

`API.md` currently advertises `top_p`, `top_k`, and `metadata`, even though the
handler does not honor them.

### CLD-007 — Cache control is not request-compatible

The beta contract permits cache control at the request level and on cacheable
system, content, and tool blocks, with `5m` and `1h` TTLs.

Current behavior:

- Top-level `cache_control` is not modeled.
- `SystemBlock.CacheControl` is parsed but discarded when system text is
  flattened.
- Message `ContentBlockParam` has no `cache_control` field.
- `ToolParam` has no `cache_control` field.
- `CacheControl` has no `ttl`.
- The Anthropic provider unconditionally sets a top-level ephemeral cache
  marker, which is not equivalent to honoring caller-selected placement/TTL.

Required change:

- Preserve explicit cache markers and TTL through the common provider model.
- Do not replace caller cache policy with an unconditional default.
- If another backend cannot emulate placement/TTL, reject the unsupported
  combination rather than claiming it was used.

### CLD-008 — Thinking configuration is lossy and under-validated

Current behavior:

- `thinking.type: "enabled"` with `budget_tokens` becomes adaptive thinking plus
  a coarse effort derived from the budget.
- The exact budget is not sent to Anthropic or other providers.
- The documented `budget_tokens >= 1024` and `< max_tokens` constraints are not
  validated at the server boundary.
- Unknown thinking types become adaptive rather than erroring.
- Invalid `display` values are not rejected.

Required change:

- Represent fixed-budget and adaptive thinking separately in
  `provider.ReasoningOptions`.
- Preserve the exact fixed budget for Anthropic backends.
- Validate the tagged union and model-specific restrictions before starting a
  response.

### CLD-009 — Context management only preserves a compaction threshold

Official edit variants include:

- `clear_tool_uses_20250919`
- `clear_thinking_20251015`
- `compact_20260112`

Current behavior:

- Only edit types beginning with `compact` have any effect.
- Compaction only retains `trigger.value`.
- `instructions` and `pause_after_compaction` are not represented.
- Clear-tool-use configuration (`trigger`, `keep`, `clear_at_least`,
  `clear_tool_inputs`, `exclude_tools`) is ignored.
- Clear-thinking `keep` is ignored.
- Applied edit details are not returned.
- A paused compaction cannot produce the documented `stop_reason:
  "compaction"`.

Required change:

- Model each edit as a strict tagged union.
- Preserve compaction instructions and pause behavior.
- Return applied edits and the correct stop reason.
- Explicitly reject strategies that the selected backend cannot support.

### CLD-010 — Message/content input union is incomplete and lossy

Supported input blocks are effectively limited to:

- `text`
- `image` with base64 or URL source
- `document` with base64, URL, or plain-text source
- `thinking`, `redacted_thinking`
- `tool_use`, `tool_result`
- `compaction`
- lossy marker handling for a few server-tool blocks

`server_tool_use`, `web_search_tool_result`, and `web_fetch_tool_result` are
additionally recognized, but only as human-readable marker text.

Missing or incomplete current input shapes include:

- `search_result`
- advisor, code-execution, bash-code-execution, text-editor-code-execution, and
  tool-search result blocks
- native `mcp_tool_use` / `mcp_tool_result`
- `container_upload`
- `mid_conv_system`
- `fallback`
- `tool_addition` / `tool_removal` (mid-conversation tool changes, Jul 24)
- Files API `source.type: "file"` for images and documents
- document `context`, `title`, and `citations`
- text-block citations
- per-block cache controls
- image transformations / oversized-image handling

Additional correctness issues:

- `source.type: "content"` is declared in the `BlockSource` comment and
  `source.type: "file"` is documented upstream, but neither is implemented by
  `toFile`; both silently produce an empty file.
- Unknown block types are silently skipped — `toMessage`'s switch has no
  default branch.
- Server-tool history is converted to human-readable marker text instead of
  round-tripped in its native shape. This can change subsequent model behavior
  and signature validation.

Required change:

- Implement a strict content-block union with lossless native payload storage.
- Preserve native blocks through the common provider layer where possible.
- Reject unsupported blocks before completion instead of dropping them.

### CLD-011 — Tool union and custom-tool fields are incomplete

Custom tool fields missing at the public Anthropic boundary:

- `strict`
- `eager_input_streaming`
- `allowed_callers`
- `cache_control`
- `input_examples`

`defer_loading` is represented for custom tools, but other documented
tool-search interactions and validation are incomplete.

Built-in tools are matched by **prefix**, not by exact type:

| Prefix | Mapped to |
|---|---|
| `text_editor*` | provider text-editor abstraction (`max_characters` honored) |
| `computer*` | provider computer abstraction (`display_width_px`/`display_height_px` honored) |
| `bash*` | provider shell abstraction |
| `tool_search_tool*` | provider tool-search abstraction |

Unrecognized types now produce a field-path error rather than being dropped,
which fixed the previous audit's silent-acceptance finding. The prefix matching
itself is now the problem:

- **`computer_toolset_20260801` is silently downgraded.** It matches the
  `computer` prefix and is converted to the legacy single computer tool, so the
  toolset's `configs` array, batch actions, default-on `zoom`, and
  `toolset_name`-tagged results are all discarded while the request returns
  200. Exact-type matching with an explicit unsupported error is required.
- `browser_toolset_20260801` (Aug 19) matches no prefix and is rejected, which
  is the right outcome, but the error text enumerates only the four legacy
  families and never mentions toolsets.

Missing built-in families include:

- code execution variants (through `code_execution_20260521`)
- memory
- web search and web fetch variants (through `web_search_20260318` /
  `web_fetch_20260318`)
- advisor (`advisor_20260301`)
- browser and computer toolsets (`*_toolset_20260801`)
- MCP toolsets

Variant-specific options are also lost, including such fields as web domain
filters/location/max uses/`return_token_budget`, code-execution callers,
strict/deferred behavior, computer zoom and per-member configs, and MCP tool
configuration. The response layer cannot emit most corresponding server-tool
result blocks, and `ContentBlock` has no `toolset_name` field.

Required change:

- Model tools as a tagged union rather than one permissive `ToolParam`.
- Preserve common custom-tool fields in `provider.Tool`.
- Add native server-tool result support.
- Return a field-specific 4xx for a known but unsupported built-in variant.

## P1: response and stop-state fidelity

### CLD-012 — Content response union is incomplete

Wingman can emit:

- `text`
- `thinking`
- `redacted_thinking`
- client `tool_use`
- `compaction`
- a limited `server_tool_use` representation for tool search

It cannot natively emit the remaining documented server/MCP/container/fallback
blocks:

- `web_search_tool_result`
- `web_fetch_tool_result`
- `advisor_tool_result`
- `code_execution_tool_result`
- `bash_code_execution_tool_result`
- `text_editor_code_execution_tool_result`
- `tool_search_tool_result`
- `mcp_tool_use`
- `mcp_tool_result`
- `container_upload`
- `fallback`

The common `provider.Content` type cannot retain these shapes, so even an
Anthropic upstream response loses them before the public response is built.

Required change:

- Add a lossless provider-native content variant or explicit common variants.
- Preserve block order, caller data, IDs, encrypted content, and result/error
  unions through sync accumulation and SSE.

### CLD-013 — Refusal and fallback details are incomplete

**Stop reasons are fixed since the previous audit.** All eight documented
values are defined in `models.go:204-215` and mapped in `convert.go:495-515`:
`end_turn`, `max_tokens`, `stop_sequence`, `tool_use`, `pause_turn`,
`compaction`, `refusal`, and `model_context_window_exceeded`. The provider
layer now carries `provider.StopReasonContextExceeded` rather than folding it
into the generic incomplete state.

What remains incomplete is the refusal/fallback payload. Current
`stop_details` carries only `type`, `category`, and `explanation`. The
documented refusal details also include:

- `fallback_credit_token` — now a union (`mode`, `token`, `remove_to_redeem`),
  not the plain string assumed by the previous audit
- `fallback_has_prefill_claim`
- `recommended_model`

These are dropped in both JSON and SSE. The `fallback` boundary content block,
the `fallbacks` request parameter (including the `"default"` mode added
Jul 24), and `usage.fallback_credit` redeemed / not-applied status are all
absent.

`stop_details.category` is passed through as an opaque string, so the newly
documented `general_harms` value round-trips without change — but no category
is validated either.

Required change:

- Extend refusal details and fallback state through `provider.Completion`.
- Model `fallbacks` and `fallback_credit_token` as request parameters, or
  reject them explicitly.
- Add non-streaming and streaming round-trip tests for every stop reason —
  `convert_test.go:107-110` covers the mapping table but not the wire shapes.

## P2: validation, errors, and tests

### CLD-014 — Validation behavior differs from the reference

Examples:

- Invalid or unknown `tool_choice.type` can be ignored.
- `tool_choice: {type:"tool"}` does not validate a non-empty name.
- Unknown effort and output-format types can be ignored.
- `metadata.user_id` length/opacity guidance is not enforced.
- Sampling ranges are not validated at the HTTP boundary.
- Current reference deprecates temperature for newer models; Wingman can still
  forward arbitrary values to non-Anthropic backends.
- Required content-block fields and role/block compatibility are not
  consistently checked.
- Error messages generally come from conversion or an upstream backend rather
  than matching Anthropic field paths and constraints.

Required change:

- Add request validation before policy/provider execution.
- Use stable field paths in errors (`messages.2.content.0...`, `tools.1...`).
- Keep error type/status/header parity, including `request-id` if Wingman intends
  full client compatibility.

### CLD-015 — Differential tests do not cover the current beta surface

Existing tests are useful for core text/tools/streaming/thinking/cache usage,
selected built-in tools, and — new since the previous audit — the stop-reason
mapping table (`convert_test.go:107-110`). Missing coverage includes:

- `model` / `messages` required-field behavior, and `max_tokens: 0` end-to-end
  (only the validation branches are reachable by unit test today)
- Explicit ignored-field detection for `top_p`, `top_k`, and metadata
- All missing top-level fields
- Response envelope field presence
- Full usage schema and iteration records
- Citations and `citations_delta`
- MCP/container/server-tool blocks
- All context-management edit variants and applied-edit responses
- Stop reason **wire shapes** (the mapping is covered; the sync/SSE payloads are
  not) and expanded refusal details
- Strict rejection of unsupported/unknown content and tools, including a
  regression test that `computer_toolset_20260801` is not silently accepted
- Beta header gating

Required change:

- Generate a field/block inventory test from the official SDK schema snapshot.
- Add golden JSON and SSE fixtures for required field presence.
- Add live differential tests for small, deterministic requests where account
  betas are available.
- Add explicit capability tests proving unsupported features fail rather than
  disappear.

## Recommended implementation order

1. **Make acceptance trustworthy.** Add `model`/`messages` presence checks and
   union validation, confirm `max_tokens: 0` per backend, and reject all
   currently ignored semantics. Replace prefix matching in `toTools` with
   exact-type matching so `computer_toolset_20260801` fails loudly instead of
   being downgraded — this is the highest-value single change in the list.
2. **Fix baseline response shape.** Add required message, text citation, usage,
   and SSE envelope fields, even when values are empty/null.
3. **Preserve native data.** Extend `provider.Content`, `Usage`,
   `StopDetails`, and options with lossless/native variants so an Anthropic
   upstream can round-trip without degradation.
4. **Complete core controls.** Implement top-level routing/speed/tier/cache,
   metadata, fixed thinking budgets, and all context-management edits.
5. **Add advanced beta families.** MCP, containers/skills, server tools,
   fallbacks, diagnostics, user profiles, and task budgets.
6. **Lock with schema and differential tests.** Update `API.md` only after each
   field is honored or explicitly documented as rejected.

## Suggested compatibility policy across backends

For each request feature, resolve one of these outcomes before generation:

1. Native support: preserve and forward it.
2. Exact safe emulation: translate it and document the mapping.
3. Unsupported: return `400 invalid_request_error` identifying the field.

Avoid best-effort acceptance for routing, budget, caching, tool execution,
context management, or safety/refusal features. Those fields affect cost,
location, side effects, or conversation correctness and must not be silently
discarded.

## Verification performed

The focused existing unit test suites pass:

```text
$ env GOCACHE=/private/tmp/wingman-go-cache go test -count=1 \
    ./server/anthropic/... ./pkg/provider/anthropic/...
ok  github.com/adrianliechti/wingman/server/anthropic          0.600s
ok  github.com/adrianliechti/wingman/pkg/provider/anthropic    0.880s
```

Every "fixed" claim in the corrections table above was checked against the code
at this revision rather than taken from the previous audit's own claims.

No code was changed by this audit; it is a documentation update only. Passing
these tests confirms the current implementation baseline; it does not close the
contract gaps above.
