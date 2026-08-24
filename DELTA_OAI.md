# OpenAI Responses create compatibility delta

Audit date: 2026-07-24

Repository revision: `06f6bd47cefa`

Target: `POST /v1/responses`, compared with OpenAI's
[Create a model response](https://developers.openai.com/api/reference/resources/responses/methods/create)
contract and
[Responses streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events).

## Executive summary

Wingman's Responses endpoint is a useful **partial, stateless compatibility
layer**. It handles the most common synchronous and streaming text flows,
structured text output, reasoning effort/summary, client-side function and
custom tools, several Codex-oriented tools, and token usage.

It is **not compatible with the complete current `responses.create` contract**.
At the top level, the current OpenAI request has 31 JSON fields including
`stream`; Wingman represents 14. Nine are substantially implemented for the
common path and five are partial. The other 17 are silently ignored because
the request decoder permits unknown fields.

The highest-risk behavior is not a missing feature by itself, but successful
HTTP 200 responses after behavior-changing fields have been discarded.
Examples include `background`, `store`, `previous_response_id`,
`conversation`, `max_tool_calls`, `top_p`, `service_tier`, moderation, and
prompt-cache controls. `web_search` is even recognized as a tool but
deliberately removed before the provider request, so a client can believe web
search was available when it was not.

The protocol-shape errors found during this audit have been corrected in the
working tree: current function-done and error events, failed/incomplete response
objects, reasoning status, terminal SSE behavior, and response defaults now
match the published shapes. Those corrections do not add any of the missing
capabilities listed below.

Protocol corrections applied:

- Added the required function `name` to
  `response.function_call_arguments.done`.
- Changed stream errors to `event: error` / `type: "error"` with present,
  nullable `code` and `param`.
- Changed failed Response errors to `{code,message}`.
- Added `incomplete_details.reason` and preserved it through the provider
  accumulator.
- Added `status` to returned reasoning items.
- Removed the Chat Completions `[DONE]` sentinel from Responses streams.
- Corrected default `top_p` to `1`, stopped fabricating
  `prompt_cache_retention: "in_memory"`, and removed legacy penalty fields.
- Read the official typed `cache_write_tokens` usage field instead of looking
  for it as an unknown SDK extension.

## Scope and method

The linked live reference and OpenAI's live OpenAPI document were treated as
the authority. The fetched OpenAPI document reported version `2.3.0`. Because
the reference page delegates nested unions to generated schema, the generated
types in Wingman's pinned official
`github.com/openai/openai-go/v3 v3.44.0` dependency were used to enumerate
those unions. The live reference wins if it differs from the dependency
snapshot.

Primary implementation files inspected:

- `server/openai/responses/models.go`
- `server/openai/responses/handler_responses.go`
- `server/openai/responses/convert.go`
- `server/openai/responses/accumulator.go`
- `server/openai/responses/handler.go`
- `server/openai/shared/convert.go`
- `server/openai/shared/error.go`
- `server/server_auth.go`
- `server/openai/responses/*_test.go`

This is a request/response/SSE wire audit. It does not require every backend to
emulate OpenAI-hosted services. A compatible proxy may reject an unavailable
capability with an OpenAI-shaped 4xx error. Accepting it and silently changing
the request is not compatible behavior.

Status terms:

- **Supported**: represented and materially honored end-to-end.
- **Partial**: accepted but lossy, backend-dependent, or incomplete.
- **Ignored**: accepted, but not applied to completion behavior.
- **Missing**: not represented and therefore normally ignored or rejected.

## Current coverage

| Area | Status | Notes |
|---|---|---|
| Basic synchronous text | Supported | `model`, string/message input, instructions, output text, status, and usage work. |
| Basic SSE text | Partial | The core lifecycle and current terminal/error shapes work; stream obfuscation remains unsupported. |
| Structured text output | Supported/partial | `text.format` and verbosity map to provider options; exact enforcement remains backend-dependent. |
| Images and files | Partial | URL/data forms work; `file_id`, detail, and cache breakpoints do not. |
| Reasoning | Partial | Effort, summary, and context work; mode is missing. |
| Function/custom tool calling | Supported/partial | Common client-executed tools work; choice enforcement and newer fields are incomplete. |
| Codex tools | Partial | Apply-patch, computer, shell, local-shell, namespace, and tool-search paths exist. |
| OpenAI-hosted tools | Missing | File search, web search, MCP, code interpreter, image generation, and programmatic tool calling are not executed. |
| Stored conversations | Missing | `store`, `previous_response_id`, and `conversation` are ignored. |
| Background responses | Missing | `background: true` still blocks and returns `background: false`. |
| Prompt caching, moderation, safety, tier | Missing | Current controls are ignored and some response defaults are fabricated. |

## Top-level request field matrix

The current create schema has 31 top-level fields including `stream`.

| Official field | Wingman state | Effective behavior |
|---|---|---|
| `model` | Supported | Selects the configured completer. Presence/enum validation is local rather than OpenAI-identical. |
| `input` | Partial | String and a substantial item subset work; the complete item/content union does not. |
| `instructions` | Supported | Converted to the leading system message. |
| `stream` | Supported/partial | Selects SSE, subject to the streaming deltas below. |
| `max_output_tokens` | Supported | Forwarded as provider maximum tokens. |
| `temperature` | Supported | Forwarded to the provider. |
| `parallel_tool_calls` | Supported | `false` disables parallel calls; response echo is populated. |
| `text` | Supported/partial | Text, JSON object/schema, and verbosity map; provider behavior can vary. |
| `context_management` | Supported | Current `compaction` threshold maps to provider compaction. |
| `include` | Partial | Only `reasoning.encrypted_content` has an effect. |
| `reasoning` | Partial | `effort`, non-disabled `summary`, and `context` map; `mode` is absent. |
| `tools` | Partial | Eight client/Codex-oriented types map; hosted and newer types do not. |
| `tool_choice` | Partial | String modes and function choices work in the common case; typed hosted choices are not enforced. |
| `truncation` | Ignored | Accepted and echoed, but not applied to the provider/context. |
| `background` | Missing/ignored | Unknown field; request remains synchronous and response says `false`. |
| `store` | Missing/ignored | Unknown field; nothing is stored and response says `false`. |
| `previous_response_id` | Missing/ignored | Unknown field; no prior response is loaded and response is `null`. |
| `conversation` | Missing/ignored | Unknown field; no conversation items are prepended or persisted. |
| `max_tool_calls` | Missing/ignored | No built-in tool-call limit is applied. |
| `top_p` | Missing/ignored | Not passed to the provider; response reports the default `1`. |
| `top_logprobs` | Missing/ignored | No logprobs are requested; response is hard-coded to `0`. |
| `metadata` | Missing/ignored | Request metadata is discarded; response is an empty object. |
| `moderation` | Missing/ignored | Requested input/output moderation behavior is not applied. |
| `prompt` | Missing/ignored | Reusable prompt ID/version/variables are not resolved. |
| `prompt_cache_key` | Missing/ignored | Cache bucketing key is discarded. |
| `prompt_cache_options` | Missing/ignored | Mode, TTL, and explicit-breakpoint behavior are not applied. |
| `prompt_cache_retention` | Missing/ignored | Request value is discarded; response reports `null` rather than fabricating a policy. |
| `safety_identifier` | Missing/ignored | Stable safety attribution is discarded. |
| `service_tier` | Missing/ignored | Routing is unaffected; response always says `default`. |
| `stream_options` | Missing/ignored | `include_obfuscation` is ignored. |
| `user` (deprecated) | Missing/ignored | Legacy user attribution/cache bucketing is discarded. |

`handleResponses` uses `json.Decoder.Decode` without
`DisallowUnknownFields`, documented-field validation, or a second decode to
reject trailing JSON. This is why most missing top-level fields still receive a
successful response.

### Include values

The current stable reference documents seven `include` values:

- `web_search_call.action.sources`
- `code_interpreter_call.outputs`
- `computer_call_output.output.image_url`
- `file_search_call.results`
- `message.input_image.image_url`
- `message.output_text.logprobs`
- `reasoning.encrypted_content`

Wingman implements only the last one. All other values are silently ignored.

### Reasoning configuration

The current reasoning object includes:

- `effort`: `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, or `max`
- `summary`: `auto`, `concise`, or `detailed`
- `context`: `auto`, `current_turn`, or `all_turns`
- `mode`: current execution mode
- deprecated `generate_summary`

Wingman maps the effort values and reduces any non-empty/non-disabled summary
value to a boolean `IncludeSummary`. It does not preserve the selected summary
mode. `context` is forwarded to the provider; the response reports the
effective mode (omitted/`auto` resolve to `all_turns` for the gpt-5.6 family,
`current_turn` for earlier models), while other explicit values are echoed
verbatim. `mode` and `generate_summary` are not represented.

## Input compatibility

### Message content

Supported common forms:

- String input and string message content
- `input_text` and historical `output_text`
- `input_image.image_url`, including HTTP(S) and data URLs
- `input_file.file_data`, `file_url`, and `filename`
- User, assistant, system, and developer roles
- String or content-array function outputs

Missing or lossy current fields:

| Current content behavior | Wingman delta |
|---|---|
| Image `file_id` | Missing |
| Image `detail` | Ignored |
| Image `prompt_cache_breakpoint` | Missing |
| File `file_id` | Missing |
| File `detail` | Ignored |
| File `prompt_cache_breakpoint` | Missing |
| Assistant message `phase` | Ignored on input |

For Codex-family follow-ups, the current schema explicitly says to preserve
assistant `phase` (`commentary` or `final_answer`); dropping it can degrade
performance. Wingman's `InputMessage` has no phase field. Generated output
messages are instead labeled `final_answer`.

Image/file HTTP URLs are eagerly downloaded with `http.Get` and converted to
provider bytes. This loses URL/file identity and differs from OpenAI's
file-reference semantics. The downloader also does not check HTTP status or
apply a response-size limit; that is an operational concern in addition to the
wire delta.

### Input item union

Wingman supports message, additional-tools, reasoning, compaction,
function-call/output, apply-patch-call/output, custom-tool-call/output,
computer-call/output, shell-call/output, local-shell-call/output, and
tool-search-call/output items.

Current official item variants missing from Wingman's union are:

- `file_search_call`
- `web_search_call`
- `image_generation_call`
- `code_interpreter_call`
- `mcp_list_tools`
- `mcp_approval_request`
- `mcp_approval_response`
- `mcp_call`
- `compaction_trigger`
- `item_reference`
- `program`
- `program_output`

These are rejected as unknown item types rather than being passed through.

## Tools and tool choice

The current tool union includes function, file search, computer,
computer-use-preview, web search, MCP, code interpreter, programmatic tool
calling, image generation, local shell, shell, custom, namespace, tool search,
web-search-preview, and apply-patch.

| Tool type | Wingman behavior |
|---|---|
| `apply_patch` | Supported through the provider text-editor abstraction. |
| `computer` | Supported through the provider computer abstraction. |
| `shell`, `local_shell` | Supported through the provider shell abstraction. |
| `namespace` | Supported for nested function/custom tools; `description` is not required (OpenAI rejects namespaces without one). |
| `tool_search` | Supported through the provider abstraction. |
| `web_search` | **Accepted and silently removed** before provider completion. |
| `file_search`, `mcp`, `code_interpreter`, `programmatic_tool_calling`, `image_generation`, `computer_use_preview`, `web_search_preview` | Rejected as invalid tool types. |

The `web_search` behavior is especially unsafe for semantic compatibility.
The source explains that hosted search is unavailable on BYOK backends, but a
successful request with no search capability can yield an ungrounded answer.
It should return a clear unsupported-capability error unless an actual search
implementation is selected.

`tool_choice` handles `none`, `auto`, `required`, a named function, and an
`allowed_tools` object. Limitations:

- Only allowed tools of type `function` contribute to provider enforcement.
- Typed hosted choices are parsed and echoed but converted to ordinary `auto`.
- Specific custom/apply-patch/shell/programmatic selections are not faithfully
  enforced.
- Invalid modes are not consistently rejected and can fall back to `auto`.

## Response object deltas

The core response envelope, text/refusal/function outputs, status, timestamps,
and usage are broadly usable. The following fields are materially inaccurate
or incomplete:

| Response area | Current Wingman behavior | Delta |
|---|---|---|
| `background` | Always `false` | Does not report requested/actual background execution. |
| `store` | Always `false` | Correctly signals local statelessness, but contradicts accepted `store:true`. |
| `previous_response_id` | Always `null` | Accepted prior-response linkage was not used. |
| `conversation` | Missing | Current conversation association cannot be returned. |
| `prompt` | Missing | Resolved prompt/template state cannot be returned. |
| `max_tool_calls` | Always `null` | Request value cannot be reflected or enforced. |
| `metadata` | Always `{}` | Request metadata is lost. |
| `service_tier` | Always `default` | Actual/requested tier is not known. |
| `top_p` | Always `1` | Correct when omitted; a caller-supplied value is still ignored. |
| `top_logprobs` | Always `0` | No requested/effective value or output logprobs. |
| `prompt_cache_retention` | `null` | Truthfully reports that Wingman has no effective OpenAI cache-retention policy. |
| `prompt_cache_options` | Missing | Effective caching configuration cannot be returned. |
| `moderation` | Always `null` | No requested/effective moderation state. |
| `safety_identifier` | Always `null` | Attribution was discarded. |
| `truncation` | Echo only | Response can claim `auto` even though it was not applied. |
| `incomplete_details` | Populated for incomplete responses | Preserves `content_filter`/`max_output_tokens`; defaults unknown incomplete causes to `max_output_tokens`. |
| failed response `error` | Emits `{code,message}` | Matches the current response-error shape. |

Legacy `frequency_penalty` and `presence_penalty` response fields were removed.

Current official reasoning output items carry `status` when returned by the
API. Wingman now includes it in terminal `response.output[]` as well as
streaming `response.output_item.added/done`.

Current function-call items also support caller information for direct versus
programmatic calls. Wingman has `namespace` but no caller/caller-ID model, so
programmatic tool calling cannot round-trip.

## Streaming deltas

The normal happy-path lifecycle is good:

- `response.created` and `response.in_progress`
- output-item added/done
- content-part added/done
- output-text delta/done
- refusal delta/done
- function arguments delta/done
- custom-tool input delta/done
- reasoning text and summary events
- `response.completed`, `response.incomplete`, and `response.failed`

Current wire status:

### OAI-SSE-001 — Function arguments done includes `name` (resolved)

The current
[`response.function_call_arguments.done`](https://developers.openai.com/api/reference/resources/responses/streaming-events#response.function_call_arguments.done)
event requires `arguments`, `item_id`, `name`, `output_index`,
`sequence_number`, and `type`. Wingman's struct, emitter, and regression test
now include `name`.

### OAI-SSE-002 — Stream error event uses `error` (resolved)

The current stream-level error object has `type: "error"` and required
`code`, `message`, `param`, and `sequence_number`. Wingman now emits
`event: error`, JSON `type: "error"`, and preserves nullable `code`/`param`
keys.

This is distinct from `response.failed`, whose payload is a full failed
Response.

### OAI-SSE-003 — Stream obfuscation is absent

Current `stream_options.include_obfuscation` controls an `obfuscation` field on
delta events, and the official schema says it is enabled by default. Wingman
ignores the option and never emits the field.

### OAI-SSE-004 — Typed terminal event without `[DONE]` (resolved)

Wingman now ends with `response.completed`, `response.incomplete`, or
`response.failed` and no longer writes the Chat Completions `data: [DONE]`
sentinel.

### Conditional event coverage

Wingman does not emit event families for unsupported features, including
background queueing, output annotations, file/web search, code interpreter,
image generation, MCP calls/listing/approval, program/program output, and
audio. This is expected while those capabilities are rejected. It becomes a
compatibility bug if the corresponding request is accepted as if supported.

## HTTP and error behavior

Ordinary handler errors use an OpenAI-like
`{"error":{"type", "code", "param", "message"}}` envelope and preserve
`Retry-After`, which is useful.

Remaining deltas:

- A configured authentication failure returns a bare HTTP 401 with an empty
  body, not an OpenAI error object.
- Unsupported top-level fields usually return 200 instead of a field-specific
  4xx error.
- Invalid enum values often fall through to defaults.
- Nameless function/custom tools disappear rather than producing a validation
  error.
- The decoder accepts a valid first JSON value without verifying end-of-body.
- No OpenAI-style request ID or rate-limit header compatibility was found.

## Local tests that currently pin obsolete or incompatible behavior

The package has useful HAR-derived coverage, but several tests describe the
present delta rather than current compatibility:

| Test | Current assertion | Required update |
|---|---|---|
| `TestStoreTrueAcceptedAndResponseEchoesStoreFalse` | Requires `store:true` to succeed while being ignored | Implement storage, or reject unsupported `store:true`. |
| `TestPreviousResponseIDAcceptedButIgnored` | Requires prior-response linkage to succeed while being ignored | Implement state lookup, or reject the field. |
| `TestToTools_SkipsHostedWebSearch` | Requires web search to disappear silently | Reject or execute it. |

HAR observations remain valuable regression fixtures, but they should not
override the current published contract without an explicit compatibility
version/profile.

## Recommended remediation

### P0 — Make the implemented subset truthful

1. Add presence-aware validation and ensure exactly one JSON request value.
2. For every documented but unsupported semantics-bearing field, return an
   OpenAI-shaped 4xx error naming the field. Do this before provider work.
3. Reject unavailable hosted tools, especially `web_search`, rather than
   silently dropping them.
4. Validate enum values, required nested fields, and discriminated unions.
5. Add a documented capability profile if Wingman intentionally targets a
   stateless/BYOK subset rather than full OpenAI behavior.

This immediately prevents false-success behavior without requiring storage or
hosted-tool infrastructure.

### P1 — Finish remaining fields on otherwise-supported paths

1. Preserve assistant `phase` and function caller data.
2. Implement `stream_options.include_obfuscation`, or explicitly reject the
   option and document the non-obfuscated profile.
3. Stop echoing `truncation` as applied until the provider actually honors it.

### P2 — Add full request semantics

1. Implement `store`, `previous_response_id`, and `conversation` together as a
   coherent persistence/state feature.
2. Add background execution and the corresponding queued/in-progress polling
   behavior.
3. Carry `top_p`, `top_logprobs`, `max_tool_calls`, metadata, moderation,
   safety identifier, service tier, and prompt-cache controls through the
   provider abstraction.
4. Implement reusable prompt references or reject `prompt`.
5. Expand the content and item unions, including `file_id` and assistant
   `phase`.
6. Add hosted tools only when their execution, result items, include fields,
   and streaming event families are all implemented.

### Suggested conformance tests

- Generate requests with the pinned official OpenAI Go types and submit them to
  Wingman.
- Decode every JSON response and SSE data object back into those official
  response/event union types.
- Maintain one golden test per top-level create field: honored behavior or a
  deliberate, OpenAI-shaped unsupported error.
- Add negative tests for unknown fields, bad enums, missing nested required
  fields, trailing JSON, and unavailable hosted tools.
- Test sync and stream terminal states for completed, incomplete, failed, and
  stream-level error paths.
- Run the same corpus across each configured backend because schema support
  can exceed an individual provider's capability.

## Verification performed

The Responses package, common provider, and provider-backend suites pass,
including new official-SDK decode checks for the corrected event/error shapes:

```text
$ env GOCACHE=/private/tmp/wingman-go-cache go test -count=1 ./server/openai/... ./pkg/provider/...
ok github.com/adrianliechti/wingman/server/openai/responses
ok github.com/adrianliechti/wingman/pkg/provider
ok github.com/adrianliechti/wingman/pkg/provider/openai
```

Passing those tests does not imply complete OpenAI conformance: as noted above,
some tests intentionally pin stateless or unsupported behavior that still
differs from the published schema.
