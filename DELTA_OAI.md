# OpenAI API compatibility delta

Audit date: 2026-08-30

Repository revision: `616ec9b1ba44`

Previous audit: 2026-07-24 (`06f6bd47cefa`), Responses only.

Targets:

- `POST /v1/responses`, compared with OpenAI's
  [Responses resource](https://developers.openai.com/api/reference/resources/responses)
  and
  [Responses streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events).
- `POST /v1/chat/completions`, compared with OpenAI's
  [Chat resource](https://developers.openai.com/api/reference/resources/chat).
- Platform changes published in the
  [API changelog](https://developers.openai.com/api/docs/changelog) since the
  previous audit.

## Executive summary

Wingman's OpenAI surface is a **partial, stateless, single-endpoint
compatibility layer** on both APIs.

For Responses it handles the common synchronous and streaming text flows,
structured text output, reasoning effort/summary/context, client-side function
and custom tools, several Codex-oriented tools, and token usage. For Chat
Completions it handles messages, tools, tool choice, reasoning effort,
verbosity, response format, stop sequences, streaming with usage, and audio
input.

Neither endpoint is compatible with the complete current contract:

- Responses `create` has 31 top-level JSON fields including `stream`; Wingman
  represents 14 — unchanged since the last audit.
- Chat `create` has 37 top-level JSON fields including `stream`; Wingman
  represents 14.
- Both resources expose additional methods (retrieve, delete, cancel, list,
  compact, input items, stored messages) that Wingman does not route at all.

The highest-risk behavior is still not a missing feature by itself, but
successful HTTP 200 responses after behavior-changing fields have been
discarded. Both handlers decode with a plain `json.Decoder.Decode` and no
`DisallowUnknownFields`, so `background`, `store`, `previous_response_id`,
`conversation`, `top_p`, `n`, `seed`, `logprobs`, `service_tier`, moderation,
and prompt-cache controls are all accepted and dropped. `web_search` is still
recognized as a Responses tool and deliberately removed before the provider
request.

The protocol-shape corrections claimed by the previous audit were re-verified
in the working tree at this revision and are all present: `name` on
`response.function_call_arguments.done`, `event: error` with
`type: "error"`, `{code,message}` failed-response errors,
`incomplete_details.reason`, reasoning item `status`, no `[DONE]` sentinel on
Responses streams, `top_p` default `1`, no fabricated
`prompt_cache_retention`, no legacy penalty fields, and typed
`cache_write_tokens`.

## Scope and method

The live reference pages and changelog were treated as the authority. The
reference pages render their nested unions client-side, so the generated types
in the official `github.com/openai/openai-go/v3` SDK were used to enumerate
those unions, as in the previous audit. `go.mod` now pins **v3.50.0** (was
v3.44.0); the enumeration below was taken from **v3.54.0**, the newest snapshot
available locally. The live reference wins where it differs from the SDK
snapshot; one such difference is flagged explicitly under Chat usage.

Primary implementation files inspected:

- `server/openai/handler.go`
- `server/openai/responses/models.go`
- `server/openai/responses/handler.go`
- `server/openai/responses/handler_responses.go`
- `server/openai/responses/convert.go`
- `server/openai/responses/accumulator.go`
- `server/openai/chat/models.go`
- `server/openai/chat/handler.go`
- `server/openai/chat/handler_completion.go`
- `server/openai/chat/convert.go`
- `server/openai/chat/accumulator.go`
- `server/openai/shared/convert.go`
- `server/openai/shared/http.go`
- `server/server_auth.go`
- `server/openai/responses/*_test.go`, `server/openai/chat/*_test.go`

This is a request/response/SSE wire audit. It does not require every backend to
emulate OpenAI-hosted services. A compatible proxy may reject an unavailable
capability with an OpenAI-shaped 4xx error. Accepting it and silently changing
the request is not compatible behavior.

Status terms:

- **Supported**: represented and materially honored end-to-end.
- **Partial**: accepted but lossy, backend-dependent, or incomplete.
- **Ignored**: accepted, but not applied to completion behavior.
- **Missing**: not represented and therefore normally ignored or rejected.

## Changes since the 2026-07-24 audit

### Corrections to the previous audit

| Previous claim | Current state |
|---|---|
| `compaction_trigger` input item is missing | **Now supported.** `InputItemTypeCompactionTrigger` is in the item union and handled in the decoder switch. |
| SDK pinned at `v3.44.0` | `go.mod` pins `v3.50.0`. |

Everything else in the previous audit's delta tables was re-verified and still
holds.

### New API surface Wingman does not cover

Derived from the changelog and from a type-level diff of SDK v3.44.0 → v3.54.0.

| Change | Source | Wingman state |
|---|---|---|
| Shell tool streaming events: `response.shell_call_command.added/delta/done`, `response.shell_call_output_content.delta/done`, with `command_index` | SDK diff | **Missing, and this one is on an otherwise-supported path.** Wingman supports `shell`/`local_shell` tools but streams them only as generic output items. |
| `web_search` tool gained `external_web_access` (offline/cache-only mode) and the `web_search_2025_08_26` type | SDK diff | Not represented; the whole tool is still dropped. |
| `service_tier` gained `fast` (Jul 30, replaced Priority Processing) and `ultrafast` (Aug 13, `gpt-5.6-sol`) | Changelog, SDK | Requested value ignored; response hard-codes `default` on both endpoints. |
| Typed MCP tool call errors (`McpToolCallError` HTTP / protocol / execution variants) | SDK diff | Not applicable while MCP tools are rejected. |
| Error variants on the input-item, item, and output-item unions | SDK diff | Not represented. |
| `response.output_text.annotation.added` annotation union (file citation, URL citation, container file citation, file path) | SDK diff | Never emitted; Responses output content always carries an empty `annotations` array. |
| Stream obfuscation `obfuscation` field now also on **Chat Completions** chunks, default-on, suppressed by `stream_options.include_obfuscation: false` | SDK diff | Missing on both endpoints. |
| `moderation` object on generation requests and on Chat chunks (Jun 4) | Changelog, SDK | Ignored on Responses; absent from Chat entirely. |
| Web search returns image results (Jun 9); `return_token_budget` web-search option (May 11) | Changelog | Not applicable while `web_search` is dropped. |
| Reusable prompt objects deprecated (Jun 3) | Changelog | Lowers the priority of implementing `prompt`; rejecting it is now the better option. |
| `prompt_cache_retention` default moved to `24h` for non-ZDR orgs (May 29); `prompt_cache_options.ttl` is the current control | Changelog, SDK | Both ignored on both endpoints. |
| Transparent image backgrounds for `gpt-image-2` (Aug 20) | Changelog | `server/openai/image` already forwards `background` verbatim through `provider.ParseBackground`; no wire gap identified. |
| GPT Transcribe / GPT Live Transcribe (Jul 28) | Changelog | Out of scope for this audit; `server/openai/audio` routes only `/audio/speech` and `/audio/transcriptions`. |

## Endpoint coverage

Wingman routes three OpenAI generation endpoints:

```text
POST /responses
POST /responses/input_tokens
POST /chat/completions
```

| Official method | Path | Wingman |
|---|---|---|
| Responses create (+ stream) | `POST /responses` | Supported (partial) |
| Responses input tokens | `POST /responses/input_tokens` | Supported |
| Responses retrieve (+ stream resume) | `GET /responses/{response_id}` | **Missing** |
| Responses delete | `DELETE /responses/{response_id}` | **Missing** |
| Responses cancel | `POST /responses/{response_id}/cancel` | **Missing** |
| Responses compact | `POST /responses/compact` | **Missing** |
| Responses input items | `GET /responses/{response_id}/input_items` | **Missing** |
| Chat create (+ stream) | `POST /chat/completions` | Supported (partial) |
| Chat retrieve | `GET /chat/completions/{completion_id}` | **Missing** |
| Chat update | `POST /chat/completions/{completion_id}` | **Missing** |
| Chat list | `GET /chat/completions` | **Missing** |
| Chat delete | `DELETE /chat/completions/{completion_id}` | **Missing** |
| Chat stored messages | `GET /chat/completions/{completion_id}/messages` | **Missing** |
| Conversations API | `/conversations/...` | **Missing** |

The five stored-Chat methods and four of the five extra Responses methods all
depend on `store`, which Wingman accepts and ignores, so they are coherent to
leave out only if `store: true` is rejected. `POST /responses/compact` is the
exception: client-side compaction is a stateless operation and Wingman already
maps `context_management.compaction` on the create path, so it is the cheapest
missing method to add.

---

# Part 1 — Responses

## Current coverage

| Area | Status | Notes |
|---|---|---|
| Basic synchronous text | Supported | `model`, string/message input, instructions, output text, status, and usage work. |
| Basic SSE text | Partial | The core lifecycle and current terminal/error shapes work; stream obfuscation remains unsupported. |
| Structured text output | Supported/partial | `text.format` and verbosity map to provider options; exact enforcement remains backend-dependent. |
| Images and files | Partial | URL/data forms work; `file_id`, detail, and cache breakpoints do not. |
| Reasoning | Partial | Effort, summary, and context work; `mode` is missing. |
| Function/custom tool calling | Supported/partial | Common client-executed tools work; choice enforcement and newer fields are incomplete. |
| Codex tools | Partial | Apply-patch, computer, shell, local-shell, namespace, and tool-search paths exist; shell streaming events do not. |
| OpenAI-hosted tools | Missing | File search, web search, MCP, code interpreter, image generation, and programmatic tool calling are not executed. |
| Stored conversations | Missing | `store`, `previous_response_id`, and `conversation` are ignored. |
| Background responses | Missing | `background: true` still blocks and returns `background: false`; no `response.queued` event and no cancel endpoint. |
| Prompt caching, moderation, safety, tier | Missing | Current controls are ignored and some response defaults are fabricated. |

## Top-level request field matrix

The current create schema has 31 top-level fields including `stream`. Wingman's
`ResponsesRequest` declares 14.

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
| `moderation` | Missing/ignored | Requested `model`/`policy` moderation behavior is not applied. |
| `prompt` | Missing/ignored | Reusable prompt ID/version/variables are not resolved. Deprecated upstream since 2026-06-03. |
| `prompt_cache_key` | Missing/ignored | Cache bucketing key is discarded. |
| `prompt_cache_options` | Missing/ignored | `mode` (`implicit`/`explicit`) and `ttl` (`30m`) are not applied. |
| `prompt_cache_retention` | Missing/ignored | Request value is discarded; response reports `null` rather than fabricating a policy. |
| `safety_identifier` | Missing/ignored | Stable safety attribution is discarded. |
| `service_tier` | Missing/ignored | Routing is unaffected; response always says `default`. Current enum is `auto`, `default`, `flex`, `scale`, `priority`, `fast`, `ultrafast`. |
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
- `mode`: current execution mode (`standard`)
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
performance. Wingman's `InputMessage` still has only `role` and `content`.
Generated output messages are labeled `final_answer` on the way out, so `phase`
round-trips in one direction only.

Image/file HTTP URLs are eagerly downloaded with `http.Get` and converted to
provider bytes. This loses URL/file identity and differs from OpenAI's
file-reference semantics. The downloader also does not check HTTP status or
apply a response-size limit; that is an operational concern in addition to the
wire delta.

### Input item union

Wingman supports message, additional-tools, reasoning, compaction,
compaction-trigger, function-call/output, apply-patch-call/output,
custom-tool-call/output, computer-call/output, shell-call/output,
local-shell-call/output, and tool-search-call/`tool_search_output` items.

Current official item variants missing from Wingman's union are:

- `file_search_call`
- `web_search_call`
- `image_generation_call`
- `code_interpreter_call`
- `mcp_list_tools`
- `mcp_approval_request`
- `mcp_approval_response`
- `mcp_call`
- `item_reference`
- `program`
- `program_output`

These are rejected as unknown item types rather than being passed through.

## Tools and tool choice

The current tool union has 16 members: function, file search, computer,
computer-use-preview, web search, MCP, code interpreter, programmatic tool
calling, image generation, local shell, shell, custom, namespace, tool search,
web-search-preview, and apply-patch.

| Tool type | Wingman behavior |
|---|---|
| `function`, `custom` | Supported. |
| `apply_patch` | Supported through the provider text-editor abstraction. |
| `computer` | Supported through the provider computer abstraction. |
| `shell`, `local_shell` | Supported through the provider shell abstraction; the new shell streaming events are not emitted. |
| `namespace` | Supported for nested function/custom tools; `description` is not required (OpenAI rejects namespaces without one). |
| `tool_search` | Supported through the provider abstraction. |
| `web_search` | **Accepted and silently removed** before provider completion. The new `external_web_access` and `web_search_2025_08_26` forms are not represented. |
| `file_search`, `mcp`, `code_interpreter`, `programmatic_tool_calling`, `image_generation`, `computer_use_preview`, `web_search_preview` | Rejected as invalid tool types. |

The `web_search` behavior is especially unsafe for semantic compatibility.
The source explains that hosted search is unavailable on BYOK backends, but a
successful request with no search capability can yield an ungrounded answer.
It should return a clear unsupported-capability error unless an actual search
implementation is selected. The same drop is applied to `web_search` promoted
from an `additional_tools` input item.

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
| `service_tier` | Always `default` | Actual/requested tier is not known; `fast`/`ultrafast` cannot be reported. |
| `top_p` | Always `1` | Correct when omitted; a caller-supplied value is still ignored. |
| `top_logprobs` | Always `0` | No requested/effective value or output logprobs. |
| `prompt_cache_retention` | `null` | Truthfully reports that Wingman has no effective OpenAI cache-retention policy. |
| `prompt_cache_options` | Missing | Effective caching configuration cannot be returned. |
| `moderation` | Always `null` | No requested/effective moderation state. |
| `safety_identifier` | Always `null` | Attribution was discarded. |
| `truncation` | Echo only | Response can claim `auto` even though it was not applied. |
| output `annotations` | Always `[]` | No URL/file/container citations are produced. |
| `incomplete_details` | Populated for incomplete responses | Preserves `content_filter`/`max_output_tokens`; defaults unknown incomplete causes to `max_output_tokens`. |
| failed response `error` | Emits `{code,message}` | Matches the current response-error shape. |

Legacy `frequency_penalty` and `presence_penalty` response fields remain
absent, which is correct.

Reasoning output items carry `status` in terminal `response.output[]` as well
as in streaming `response.output_item.added/done`.

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
- stream-level `event: error`

Wingman emits 23 of the 58 event types in the current union.

### OAI-SSE-001 — Function arguments done includes `name` (resolved)

Verified at this revision: `FunctionCallArgumentsDoneEvent` carries `type`,
`sequence_number`, `item_id`, `output_index`, `name`, and `arguments`.

### OAI-SSE-002 — Stream error event uses `error` (resolved)

Verified at this revision: `handler_responses.go` writes
`event: error` with JSON `type: "error"` and preserves nullable `code`/`param`.

This is distinct from `response.failed`, whose payload is a full failed
Response.

### OAI-SSE-003 — Stream obfuscation is absent

Current `stream_options.include_obfuscation` controls an `obfuscation` field on
delta events, and the official schema says it is enabled by default. Wingman
ignores the option and never emits the field. As of SDK v3.54.0 this applies to
Chat Completions chunks as well.

### OAI-SSE-004 — Typed terminal event without `[DONE]` (resolved)

Verified at this revision: no `[DONE]` sentinel exists anywhere in
`server/openai/responses`; streams end with `response.completed`,
`response.incomplete`, or `response.failed`.

### OAI-SSE-005 — Shell tool streaming events are missing (new)

The current event union added a dedicated family for shell tool calls:

- `response.shell_call_command.added`
- `response.shell_call_command.delta`
- `response.shell_call_command.done`
- `response.shell_call_output_content.delta`
- `response.shell_call_output_content.done`

They carry `command_index` and, on the done event, a
`{outcome: exit|timeout}` output object. Unlike the other missing families,
this one sits on a path Wingman *does* support: `shell` and `local_shell` tools
are accepted, converted, and returned as output items. A client that follows
the current streaming contract will not see incremental command or output
content from Wingman.

### OAI-SSE-006 — Output-text annotation events are missing (new)

`response.output_text.annotation.added` now carries a four-member annotation
union (URL citation, file citation, container file citation, file path).
Wingman never emits the event and always returns an empty `annotations` array.

### Conditional event coverage

Wingman does not emit event families for unsupported features, including
background queueing (`response.queued`), file/web search, code interpreter,
image generation, MCP calls/listing/approval, `response.compaction`, and audio.
This is expected while those capabilities are rejected. It becomes a
compatibility bug if the corresponding request is accepted as if supported.

---

# Part 2 — Chat Completions

This surface was not covered by the previous audit.

## Current coverage

| Area | Status | Notes |
|---|---|---|
| Basic synchronous text | Supported | `model`, messages, content, usage, and finish reason work. |
| Basic SSE text | Supported/partial | Deltas, tool-call chunks, finish chunk, `stream_options.include_usage`, and the `[DONE]` sentinel are correct; `obfuscation` is missing. |
| Structured output | Supported | `response_format` text/json_object/json_schema map to provider schema options. |
| Reasoning effort / verbosity | Supported | Full current enums, including `xhigh` and `max`. |
| Function tools | Supported | `tools[].function` with `strict` and normalized schema. |
| Tool choice | Supported/partial | String modes, legacy named-function object, and both `allowed_tools` shapes decode; only function names reach provider enforcement. |
| Images, files, audio input | Partial | `image_url.url`, `file.file_data`/`filename`, and `input_audio` work; `detail`, `file_id`, and `prompt_cache_breakpoint` do not. |
| Audio output | Missing | `modalities` and `audio` request fields and the response `message.audio` field are absent. |
| Logprobs | Missing | Request fields ignored; the required `choices[].logprobs` response field is not emitted at all. |
| Multiple choices | Missing | `n` is ignored; exactly one choice is returned, and `index` is never set. |
| Stored completions | Missing | `store` and `metadata` are ignored; the four stored-completion methods are unrouted. |
| Moderation, safety, tier, caching | Missing | All ignored; `service_tier` is hard-coded. |

## Top-level request field matrix

The current create schema has 37 top-level fields including `stream`. Wingman's
`ChatCompletionRequest` declares 14.

| Official field | Wingman state | Effective behavior |
|---|---|---|
| `model` | Supported | Selects the configured completer. |
| `messages` | Supported/partial | See the content-part table below. |
| `stream` | Supported | Selects SSE. |
| `stream_options` | Partial | `include_usage` is honored; `include_obfuscation` is not represented. |
| `stop` | Supported | String, `[]string`, and `[]any` forms all collapse to provider stop sequences. |
| `tools` | Partial | `function` supported; `custom` returns an OpenAI-shaped 400 naming `tools[i].type`. |
| `tool_choice` | Supported/partial | Four decode shapes; only `function` entries reach the provider. |
| `parallel_tool_calls` | Supported | `false` sets `DisableParallelToolCalls`. |
| `temperature` | Supported | Forwarded to the provider. |
| `max_completion_tokens` | Supported | Forwarded as provider maximum tokens. |
| `max_tokens` (deprecated) | Supported | Used only when `max_completion_tokens` is absent. |
| `response_format` | Supported | `text`, `json_object`, and `json_schema` map to provider schema options. |
| `reasoning_effort` | Supported | Full enum; `none` maps to disabled reasoning, everything else to adaptive. |
| `verbosity` | Supported | `low`/`medium`/`high` map to provider output options. |
| `top_p` | Missing/ignored | Commented out in the request struct; silently dropped. |
| `n` | Missing/ignored | Always exactly one choice is returned. |
| `seed` | Missing/ignored | No determinism control reaches the provider. |
| `logprobs`, `top_logprobs` | Missing/ignored | No logprobs are requested or returned. |
| `logit_bias` | Missing/ignored | Discarded. |
| `frequency_penalty`, `presence_penalty` | Missing/ignored | Discarded. |
| `store` | Missing/ignored | Nothing is stored; no stored-completion methods exist. |
| `metadata` | Missing/ignored | Discarded and not echoed. |
| `moderation` | Missing/ignored | No moderation object on the request or the response. |
| `prediction` | Missing/ignored | Predicted Outputs are not forwarded. |
| `modalities`, `audio` | Missing/ignored | Audio *output* is not supported; audio input is. |
| `prompt_cache_key` | Missing/ignored | Cache bucketing key is discarded. |
| `prompt_cache_options` | Missing/ignored | `mode`/`ttl` are not applied. |
| `prompt_cache_retention` | Missing/ignored | `in_memory`/`24h` are not applied. |
| `safety_identifier` | Missing/ignored | Attribution is discarded. |
| `service_tier` | Missing/ignored | Response hard-codes `default`; `fast`/`ultrafast` cannot be requested or reported. |
| `web_search_options` | Missing/ignored | No hosted search; unlike Responses this is not even recognized. |
| `user` (deprecated) | Missing/ignored | Discarded. |
| `functions`, `function_call` (deprecated) | Missing/ignored | Legacy function calling is not accepted; `finish_reason: "function_call"` is never produced. |

`handleChatCompletion` decodes with a plain `json.Decoder.Decode`, so every
row marked missing/ignored above still returns HTTP 200.

## Message content deltas

Official content parts are `text`, `image_url`, `input_audio`, and `file`.
Wingman decodes all four plus `refusal`.

| Current content behavior | Wingman delta |
|---|---|
| `image_url.detail` | Ignored |
| `image_url.prompt_cache_breakpoint` | Missing |
| `file.file_id` | Missing |
| `file.prompt_cache_breakpoint` | Missing |
| Assistant `audio` output part | Missing |

`ChatCompletionMessage.UnmarshalJSON` tries a string-content shape first and an
array-content shape second, and **returns `nil` when both fail**. A malformed
message therefore decodes to an empty message with an empty role, which is then
rejected downstream by `toMessages` with an OpenAI-shaped `messages[i].role`
error. The resulting `param` and message do not point at the real problem.

## Response object deltas

| Response area | Current Wingman behavior | Delta |
|---|---|---|
| `choices[].logprobs` | Field absent from the struct | Officially required (nullable); Wingman omits the key entirely. |
| `choices[].index` | Always the zero value | Correct for one choice, but never explicitly set. |
| `service_tier` | Always `default` | Requested tier is unknown; not emitted at all on the streaming finish chunk. |
| `system_fingerprint` | `*string` without `omitempty` | Always serialized, normally as `null`. |
| `metadata` | Missing | Cannot echo stored-completion metadata. |
| `moderation` | Missing | No moderation results on the object or the chunk. |
| `message.annotations` | Always `[]` | No URL citations are produced. |
| `message.audio` | Missing | No audio output. |
| `message.function_call` | Missing | Deprecated upstream; acceptable. |
| `usage.compute_units` | Missing | Documented in the live Chat reference; not present in SDK v3.54.0, so treat as low-confidence until the SDK catches up. |
| `usage.prompt_tokens_details` | `cached_tokens` + `cache_write_tokens` | Matches the Responses-side handling; `audio_tokens` is not reported. |
| `usage.completion_tokens_details` | `reasoning_tokens` only | `audio_tokens`, `accepted_prediction_tokens`, and `rejected_prediction_tokens` are missing. |
| `finish_reason` | `stop`/`length`/`tool_calls`/`content_filter` | Complete except deprecated `function_call`. |

## Streaming deltas

The Chat stream shape is largely correct: `chat.completion.chunk` objects, a
role/content delta sequence, indexed tool-call deltas, an empty-arguments chunk
for argumentless calls, a finish chunk, an optional usage chunk gated on
`stream_options.include_usage`, and the `data: [DONE]` sentinel — which, unlike
on Responses, *is* part of the Chat contract.

Remaining deltas:

- `obfuscation` is never emitted and `include_obfuscation` is ignored.
- The `moderation` chunk is never emitted.
- `service_tier` is set on the content and tool-call chunks but omitted on the
  finish chunk, so a single stream is internally inconsistent.
- Stream errors are written as a bare `{"error":{...}}` data frame with no
  `event:` name, which matches Chat convention but carries no `code`/`param`.

---

# HTTP and error behavior

Ordinary handler errors on both endpoints use an OpenAI-like
`{"error":{"type", "code", "param", "message"}}` envelope and preserve
`Retry-After`, which is useful.

Remaining deltas:

- A configured authentication failure returns a bare HTTP 401 with an empty
  body, not an OpenAI error object.
- Unsupported top-level fields usually return 200 instead of a field-specific
  4xx error, on both endpoints.
- Invalid enum values often fall through to defaults.
- Nameless function/custom tools disappear rather than producing a validation
  error.
- Both decoders accept a valid first JSON value without verifying end-of-body.
- No OpenAI-style request ID or rate-limit header compatibility was found.
- An unknown model returns HTTP 404 with `type: "not_found_error"`, an empty
  `code`, and the message `completer not found: <id>`. OpenAI returns
  `type: "invalid_request_error"`, `code: "model_not_found"`, `param: "model"`,
  and a message naming the model.

# Local tests that currently pin obsolete or incompatible behavior

The packages have useful HAR-derived coverage, but several tests describe the
present delta rather than current compatibility:

| Test | Current assertion | Required update |
|---|---|---|
| `TestStoreTrueAcceptedAndResponseEchoesStoreFalse` | Requires `store:true` to succeed while being ignored | Implement storage, or reject unsupported `store:true`. |
| `TestPreviousResponseIDAcceptedButIgnored` | Requires prior-response linkage to succeed while being ignored | Implement state lookup, or reject the field. |
| `TestToTools_SkipsHostedWebSearch` | Requires web search to disappear silently | Reject or execute it. |

HAR observations remain valuable regression fixtures, but they should not
override the current published contract without an explicit compatibility
version/profile.

There is no equivalent coverage on the Chat side asserting the ignored-field
behavior, so the Chat deltas are undocumented in tests as well as in code.

# Recommended remediation

### P0 — Make the implemented subset truthful

1. Add presence-aware validation on both handlers and ensure exactly one JSON
   request value.
2. For every documented but unsupported semantics-bearing field, return an
   OpenAI-shaped 4xx error naming the field. Do this before provider work.
3. Reject unavailable hosted tools, especially Responses `web_search` and Chat
   `web_search_options`, rather than silently dropping them.
4. Validate enum values, required nested fields, and discriminated unions.
5. Fix `ChatCompletionMessage.UnmarshalJSON` to return a real decode error
   instead of `nil`, so malformed messages produce an accurate `param`.
6. Add a documented capability profile if Wingman intentionally targets a
   stateless/BYOK subset rather than full OpenAI behavior.

This immediately prevents false-success behavior without requiring storage or
hosted-tool infrastructure.

### P1 — Finish remaining fields on otherwise-supported paths

1. Emit the shell tool streaming event family (OAI-SSE-005); shell tools are
   already supported, so this is a gap inside a shipped feature.
2. Preserve assistant `phase` on input and function caller data.
3. Emit `choices[].logprobs` (as `null`) on Chat, and set `service_tier`
   consistently across every chunk in a stream.
4. Implement `stream_options.include_obfuscation` on both endpoints, or
   explicitly reject the option and document the non-obfuscated profile.
5. Carry `top_p` on both endpoints — it is a plain sampling parameter that most
   providers already accept.
6. Stop echoing `truncation` as applied until the provider actually honors it.
7. Add `POST /responses/compact`; it is stateless and the compaction mapping
   already exists.

### P2 — Add full request semantics

1. Implement `store`, `previous_response_id`, and `conversation` together as a
   coherent persistence/state feature, along with the retrieve/delete/list
   methods on both resources.
2. Add background execution with `response.queued`, the retrieve-stream resume
   path, and `POST /responses/{id}/cancel`.
3. Carry `top_logprobs`, `max_tool_calls`, `n`, `seed`, metadata, moderation,
   safety identifier, service tier (including `fast`/`ultrafast`), and
   prompt-cache controls through the provider abstraction.
4. Reject `prompt` explicitly rather than implementing it; reusable prompt
   objects were deprecated on 2026-06-03.
5. Expand the content and item unions, including `file_id`, `detail`,
   `prompt_cache_breakpoint`, and assistant `phase`.
6. Add Chat audio output (`modalities`, `audio`, `message.audio`) alongside the
   audio input path that already exists.
7. Add hosted tools only when their execution, result items, include fields,
   and streaming event families are all implemented.

### Suggested conformance tests

- Generate requests with the pinned official OpenAI Go types and submit them to
  Wingman, for both `responses` and `chat`.
- Decode every JSON response and SSE data object back into those official
  response/event union types.
- Maintain one golden test per top-level create field on each endpoint:
  honored behavior or a deliberate, OpenAI-shaped unsupported error.
- Add negative tests for unknown fields, bad enums, missing nested required
  fields, trailing JSON, malformed message content, and unavailable hosted
  tools.
- Test sync and stream terminal states for completed, incomplete, failed, and
  stream-level error paths on both endpoints.
- Run the same corpus across each configured backend because schema support
  can exceed an individual provider's capability.

# Verification performed

```text
$ env GOCACHE=/private/tmp/wingman-go-cache go test -count=1 ./server/openai/...
?   github.com/adrianliechti/wingman/server/openai          [no test files]
ok  github.com/adrianliechti/wingman/server/openai/audio     1.081s
ok  github.com/adrianliechti/wingman/server/openai/chat      1.583s
?   github.com/adrianliechti/wingman/server/openai/embeddings [no test files]
?   github.com/adrianliechti/wingman/server/openai/image     [no test files]
?   github.com/adrianliechti/wingman/server/openai/models    [no test files]
?   github.com/adrianliechti/wingman/server/openai/realtime  [no test files]
ok  github.com/adrianliechti/wingman/server/openai/responses 0.601s
?   github.com/adrianliechti/wingman/server/openai/shared    [no test files]
```

No code was changed by this audit; it is a documentation update only.

Passing those tests does not imply complete OpenAI conformance: as noted above,
some tests intentionally pin stateless or unsupported behavior that still
differs from the published schema, and the Chat package has no tests covering
ignored top-level fields at all.
