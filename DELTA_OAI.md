# OpenAI API compliance TODO

Last review: 2026-09-05 (`1313d4002804`, working tree)

Open items for `POST /v1/responses` and `POST /v1/chat/completions` against
the OpenAI Responses and Chat references. Each item is a gap; nothing that
already works is listed. Priorities: **P0** wrong answer or silent 200,
**P1** gap on an otherwise supported path, **P2** missing feature.

## Request validation

- [ ] P0 Reject unknown top-level fields and trailing JSON on both endpoints
      (plain `json.Decoder.Decode` today).
- [ ] P0 Reject or execute `web_search` instead of silently dropping it
      (also when promoted from `additional_tools`).
- [ ] P1 Return a real decode error from `ChatCompletionMessage.UnmarshalJSON`
      instead of `nil`, so malformed messages get an accurate `param`.
- [ ] P1 Validate enum values and required nested fields instead of falling
      back to defaults.
- [ ] P1 Return `type: "invalid_request_error"`, `code: "model_not_found"`,
      `param: "model"` for an unknown model (currently `not_found_error`).
- [ ] P2 Return an OpenAI error body on authentication failure (bare 401
      today).

## Responses: request fields

- [ ] P1 Apply `truncation` instead of echoing it (the adapter always sends
      `auto`).
- [ ] P1 Carry `top_p` to the provider.
- [ ] P1 Preserve the requested `reasoning.summary` mode (`auto` / `concise`
      / `detailed`) instead of a boolean.
- [ ] P1 Add `reasoning.mode`.
- [ ] P1 Honor or reject `stream_options.include_obfuscation`.
- [ ] P2 Honor or reject: `background`, `store`, `previous_response_id`,
      `conversation`, `max_tool_calls`, `top_logprobs`, `metadata`,
      `moderation`, `prompt_cache_key`, `prompt_cache_options`,
      `prompt_cache_retention`, `safety_identifier`, `service_tier`, `user`.
- [ ] P2 Reject `prompt` explicitly (deprecated upstream).
- [ ] P2 Honor the remaining `include` values (`web_search_call.action.sources`,
      `code_interpreter_call.outputs`, `computer_call_output.output.image_url`,
      `file_search_call.results`, `message.input_image.image_url`,
      `message.output_text.logprobs`).

## Responses: input

- [ ] P1 Support image / file `file_id`, `detail`, `prompt_cache_breakpoint`.
- [ ] P1 Limit size and check status when downloading image / file URLs.
- [ ] P2 Accept the hosted-tool items (`file_search_call`, `web_search_call`,
      `image_generation_call`, `code_interpreter_call`, `mcp_*`,
      `item_reference`, `program`, `program_output`) or reject them with a
      field-specific error (currently rejected as unknown types).

## Responses: tools

- [ ] P1 Enforce typed hosted `tool_choice` entries and non-`function`
      `allowed_tools` entries instead of degrading to `auto`.
- [ ] P1 Carry function `caller` data so programmatic tool calling can
      round-trip.
- [ ] P2 Support `web_search.external_web_access`.
- [ ] P2 Support or explicitly reject `file_search`, `mcp`,
      `code_interpreter`, `programmatic_tool_calling`, `image_generation`,
      `computer_use_preview`, `web_search_preview` (rejected today).

## Responses: response object

- [ ] P1 Add the fields the live API now returns: `presence_penalty`,
      `frequency_penalty`, `reasoning.mode`, `tool_usage`,
      `tools[].output_schema`; `top_p` default is `0.98`.
- [ ] P1 Report the requested `service_tier`, `top_p`, `top_logprobs`,
      `metadata`, `max_tool_calls`, `safety_identifier`, `moderation` instead
      of fixed values.
- [ ] P1 Distinguish `incomplete_details.reason` values instead of defaulting
      to `max_output_tokens`.
- [ ] P1 Use one id scheme for sync and stream (`resp_` on stream, upstream
      id on sync).
- [ ] P2 Produce output `annotations` (URL / file citations).
- [ ] P2 Add `prompt_cache_options` and `conversation` to the envelope.

## Responses: streaming

- [ ] P1 Emit the shell tool event family
      (`response.shell_call_command.added/delta/done`,
      `response.shell_call_output_content.delta/done`).
- [ ] P1 Emit `obfuscation` on delta events when requested.
- [ ] P2 Emit `response.output_text.annotation.added`.
- [ ] P2 Emit `response.queued`, `response.compaction`, hosted-tool, MCP and
      audio event families once the features exist.

## Responses: endpoints

- [ ] P2 `POST /responses/compact` (stateless; compaction mapping exists).
- [ ] P2 Retrieve / delete / cancel / input_items and the Conversations API
      together with `store`.

## Chat: request fields

- [ ] P1 Carry `top_p` to the provider.
- [ ] P1 Honor or reject `stream_options.include_obfuscation`.
- [ ] P1 Report `content_filter` only for filtered output; a Responses-backend
      refusal must be `finish_reason: "stop"` with `refusal` set.
- [ ] P2 Honor or reject: `n`, `seed`, `logprobs`, `top_logprobs`,
      `logit_bias`, `frequency_penalty`, `presence_penalty`, `store`,
      `metadata`, `moderation`, `prediction`, `prompt_cache_key`,
      `prompt_cache_options`, `prompt_cache_retention`, `safety_identifier`,
      `service_tier`, `user`.
- [ ] P2 Reject or execute `web_search_options`.
- [ ] P2 Reject `functions` / `function_call` explicitly.
- [ ] P2 Audio output (`modalities`, `audio`, `message.audio`).
- [ ] P2 Report usage for responses cut by the emulated `stop` on reasoning
      models (the upstream stream is cancelled before its usage arrives).

## Chat: message content

- [ ] P1 Honor `image_url.detail` and `file.detail`.
- [ ] P1 Support `file.file_id`, `image_url.prompt_cache_breakpoint`,
      `file.prompt_cache_breakpoint`.
- [ ] P2 Accept the assistant `audio` part.

## Chat: response object

- [ ] P1 Emit `choices[].logprobs` (as `null`).
- [ ] P1 Emit `service_tier` on the finish chunk (present on other chunks).
- [ ] P1 Add `code` / `param` to streamed error frames.
- [ ] P2 Add `metadata`, `moderation`, `usage.*.audio_tokens`,
      `accepted_prediction_tokens`, `rejected_prediction_tokens`.
- [ ] P2 Emit the `moderation` chunk.
- [ ] P2 Produce `annotations` (URL citations).
- [ ] P2 Support `n` > 1.

## Chat: endpoints

- [ ] P2 Retrieve / update / list / delete / messages together with `store`.

## Tests to change once the items above land

- [ ] `TestStoreTrueAcceptedAndResponseEchoesStoreFalse` pins ignoring
      `store: true`.
- [ ] `TestPreviousResponseIDAcceptedButIgnored` pins ignoring
      `previous_response_id`.
- [ ] `TestToTools_SkipsHostedWebSearch` pins dropping `web_search`.
- [ ] Add per-field golden tests: honored behavior or an OpenAI-shaped
      unsupported error, on both endpoints.
