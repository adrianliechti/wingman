# OpenAI API — TODO

Gaps between Wingman's `POST /v1/responses` + `POST /v1/chat/completions`
and the OpenAI Responses and Chat references. Delete an item once it lands;
add new gaps where they belong.

**P0** wrong answer or silent 200 · **P1** broken on a supported path ·
**P2** missing feature. Code paths: `server/openai/{responses,chat}` (wire
format) → `pkg/provider` (shared types) → `pkg/provider/*` (backends).

## P0

- [ ] **Strict decoding (both).** Reject unknown top-level fields and
      trailing JSON; both handlers use a plain `json.NewDecoder(...).Decode`
      (`responses/handler_responses.go`, `chat/handler_completion.go`).
- [ ] **`web_search` (Responses).** Execute it or reject it; it is silently
      dropped, also when promoted from `additional_tools`
      (`responses/convert.go`, `toTools`). Update the pinning tests below.

## P1

### Both endpoints

- [ ] **Forced `tool_choice` on Claude.** `required` / a named function on
      Opus 5.5, Sonnet 5.5, Fable/Mythos 5.1 returns 400 without
      `param: "tool_choice"`; add it.
- [ ] **Enum / nested validation.** Reject invalid enum values and missing
      required nested fields instead of falling back to defaults.
- [ ] **`stream_options.include_obfuscation`.** Honor (emit `obfuscation` on
      delta events) or reject.
- [ ] **URL downloads.** Bound size and time in `server/files.FromURL`
      (default client, unbounded `io.ReadAll`).

### Responses

- [ ] **`truncation`.** Apply the requested value; the adapter sends `auto`
      unless a `configuration_update` forces `disabled`
      (`pkg/provider/openai/responder.go`).
- [ ] **`reasoning.summary`.** Keep `auto` / `concise` / `detailed` instead of
      collapsing to `IncludeSummary` (`handler_responses.go`).
- [ ] **`reasoning.mode`.** Accept and forward non-default modes; the
      response currently reports `standard`.
- [ ] **Input files.** Support image/file `file_id` and `detail`.
- [ ] **`tool_choice` / `allowed_tools`.** Enforce typed hosted entries and
      non-`function` `allowed_tools` instead of degrading to `auto`
      (`convert.go`, `toToolOptions`).
- [ ] **Programmatic tool calling.** Carry function `caller` data so it
      round-trips.
- [ ] **Response fields.** Honor non-default penalties and function
      `output_schema`. Echo the requested `service_tier`,
      `top_logprobs`, `metadata`, `max_tool_calls`, `safety_identifier`,
      `moderation` instead of fixed values.
- [ ] **Shell events.** Emit `response.shell_call_command.added/delta/done`
      and `response.shell_call_output_content.delta/done`.

### Chat

- [ ] **Message decode errors.** `ChatCompletionMessage.UnmarshalJSON`
      (`chat/models.go`) returns `nil` on malformed input; return the error
      with an accurate `param`.
- [ ] **Refusal finish reason.** A refusal from a Responses backend must be
      `finish_reason: "stop"` with `refusal` set; reserve `content_filter` for
      filtered output.
- [ ] **Content parts.** Honor `image_url.detail`, `file.detail`,
      `file.file_id`; apply `prompt_cache_breakpoint` to image and file parts
      (text only today, `chat/convert.go`).
- [ ] **Response fields.** Emit `choices[].logprobs: null`; `service_tier`
      on the finish and usage chunks; `code` / `param` on streamed error
      frames.

## P2

### Both endpoints

- [ ] **Annotations.** Produce URL / file citations
      (`response.output_text.annotation.added` on Responses).

### Responses

- [ ] **Unsupported fields.** Honor or reject `background`, `store`,
      `previous_response_id`, `conversation`, `max_tool_calls`,
      `top_logprobs`, `metadata`, `moderation`, `safety_identifier`,
      `service_tier`, `user`; reject deprecated `prompt`.
- [ ] **`include` values.** `web_search_call.action.sources`,
      `code_interpreter_call.outputs`,
      `computer_call_output.output.image_url`, `file_search_call.results`,
      `message.input_image.image_url`, `message.output_text.logprobs`.
- [ ] **Hosted-tool input items.** Accept or reject with a field-specific
      error: `file_search_call`, `web_search_call`, `image_generation_call`,
      `code_interpreter_call`, `mcp_*`, `item_reference`, `program`,
      `program_output` (generic unknown-type error today).
- [ ] **Hosted tools.** Support or explicitly reject `file_search`, `mcp`,
      `code_interpreter`, `programmatic_tool_calling`, `image_generation`,
      `computer_use_preview`, `web_search_preview`;
      `web_search.external_web_access`.
- [ ] **Envelope.** Add `conversation`; emit `prompt_cache_options` when not
      requested (verify the live API always returns it).
- [ ] **Stream events.** `response.queued`, `response.compaction`, hosted-tool,
      MCP and audio families once the features exist.
- [ ] **Endpoints.** `POST /responses/compact` (stateless; compaction mapping
      exists); retrieve / delete / cancel / input_items and Conversations,
      together with `store`.

### Chat

- [ ] **Unsupported fields.** Honor or reject `n`, `seed`, `logprobs`,
      `top_logprobs`, `logit_bias`, `frequency_penalty`, `presence_penalty`,
      `store`, `metadata`, `moderation`, `prediction`, `safety_identifier`,
      `service_tier`, `user`; `web_search_options`; `functions` /
      `function_call`.
- [ ] **Audio.** `modalities`, `audio`, `message.audio`, the assistant `audio`
      input part, `usage.*.audio_tokens`.
- [ ] **Response fields.** `metadata`, `moderation` (+ its chunk),
      `accepted_prediction_tokens`, `rejected_prediction_tokens`; `n` > 1.
- [ ] **Usage after emulated `stop`.** Reasoning models cut by the emulated
      stop lose usage (upstream stream cancelled first).
- [ ] **Endpoints.** Retrieve / update / list / delete / messages, together
      with `store`.

### Images (`/images/generations`, `/images/edits`)

- [ ] **Native `markup`.** The spec, the Go SDK (v3.91+) and the reference
      list `markup` for GPT Image 2.5, but the live edits endpoint answered
      `Unknown parameter: 'markup'` on 2026-11-04 (multipart and JSON). The
      adapter emulates markup for every model. Add native forwarding in
      `pkg/provider/openai/renderer.go` once live support is verified.
- [ ] **JSON edit body.** Accept `images[].image_url` / `file_id`,
      `mask` and `markup` objects (multipart only today).
- [ ] **Unsupported fields.** Honor or reject `input_fidelity` (1.x models
      only), `moderation`, `output_compression`, `n` (always one image),
      `stream` / `partial_images`, `safety_identifier` / `user`.
- [ ] **Response envelope.** Return `created`, `background`, `output_format`,
      `quality`, `size` and `usage` (image / text token details); carry
      `usage` on `provider.Rendering` so the budget service can bill renders.
- [ ] **Model list.** `gpt-image-1` shut down 2026-10-23; the size table in
      the adapter still special-cases it.

## Tests

- [ ] Update when the items above land:
      `TestStoreTrueAcceptedAndResponseEchoesStoreFalse` (ignores `store`),
      `TestPreviousResponseIDAcceptedButIgnored`,
      `TestToTools_SkipsHostedWebSearch` and
      `TestAdditionalToolsInputReachesProvider` (drop `web_search`).
- [ ] Per-field golden tests on both endpoints: honored behavior or an
      OpenAI-shaped unsupported error.
