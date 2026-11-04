# Gemini GenerateContent API — TODO

Gaps between Wingman's `models/{model}:generateContent`,
`:streamGenerateContent`, `:countTokens` and the Gemini API reference (v1beta
discovery document). Delete an item once it lands; add new gaps where they
belong.

**P0** wrong answer or silent 200 · **P1** broken on a supported path ·
**P2** missing feature. Code paths: `server/gemini` (wire format) →
`pkg/provider` (shared types) → `pkg/provider/*` (backends).

## P0

- [ ] **`safetySettings`.** Honor or reject; parsed and ignored.
- [ ] **Strict decoding.** Reject unknown fields and trailing JSON
      (`server/gemini/decode.go`, `decodeRequest`).
- [ ] **Empty tools.** Reject a tool object holding only unsupported built-in
      tools instead of decoding it to an empty tool (`convert.go`, `toTools`).
- [ ] **Blocked prompts.** Return `promptFeedback` with no candidates instead
      of an empty candidate.

## P1

### Request

- [ ] **Validation.** Require `contents`; validate `Content.role` (coerced to
      `user` today), single-field `Part` unions, enums, numeric ranges, name
      constraints, schema mutual exclusion.
- [ ] **API-key auth.** Accept `key=` and `x-goog-api-key` when an authorizer
      is configured (`pkg/auth/static` accepts Bearer only).
- [ ] **Sampling.** Carry `seed`, `presencePenalty`,
      `frequencyPenalty` (add to `CompleteOptions`) or reject them; honor
      `candidateCount` or reject values other than 1.
- [ ] **`responseMimeType`.** Reject anything but JSON (`text/x.enum` and
      others are ignored).

### Thinking

- [ ] **Exact `thinkingBudget`.** Pass the budget to Gemini backends instead
      of a coarse level.
- [ ] **`thinkingBudget: 0`.** Disable thinking on its own; today only
      together with `includeThoughts`. (Sonnet 5.5 can only go down to
      `between_tools`.)
- [ ] **Level vs. budget.** Reject conflicting combinations instead of
      letting the level win.

### Function calling

- [ ] **`MALFORMED_FUNCTION_CALL`.** Surface it instead of replacing
      unparseable arguments with `{}`.
- [ ] **Schema dialects.** Convert `parameters` (OpenAPI: uppercase `type`,
      `nullable`) like `responseSchema`, keep it distinct from
      `parametersJsonSchema`, and reject both being set (`parameters` wins
      silently today).

### Parts and response

- [ ] **Output media.** Emit generated `inlineData` / `fileData` parts
      (`toContent` drops `File`).
- [ ] **`functionResponse.parts[].fileData`.** Keep URIs as URIs (stored as
      bytes today).
- [ ] **`finishReason`.** Preserve the upstream value (reduced to `STOP`,
      `MAX_TOKENS`, `SAFETY`, `OTHER` in `toFinishReason`).
- [ ] **Envelope.** Preserve upstream `responseId` and `modelVersion`; emit
      `safetyRatings`, `finishMessage`, `tokenCount`.

### Streaming

- [ ] **Mid-stream errors.** Close the array with `]` after an error, and
      define the error frame separately from `GenerateContentResponse`
      (`handler_generate.go`).

## P2

- [ ] **`systemInstruction` parts.** Honor or reject non-text parts (dropped).
- [ ] **Request fields.** Honor or reject `cachedContent`, `serviceTier`,
      `store`.
- [ ] **Generation config.** Honor or reject `responseLogprobs`, `logprobs`,
      `responseModalities`, `mediaResolution`; add `speechConfig`,
      `imageConfig`, `audioTranscriptionConfig`,
      `enableEnhancedCivicAnswers`, `enableAffectiveDialog`,
      `responseFormat`, `translationConfig`.
- [ ] **Function calling fields.** `FunctionDeclaration.response`,
      `responseJsonSchema`, `behavior`; `FunctionResponse.scheduling`,
      `willContinue`; `ToolConfig.retrievalConfig`,
      `includeServerSideToolInvocations`.
- [ ] **Built-in tools.** Support or reject `codeExecution`, `googleSearch`,
      `googleSearchRetrieval`, `computerUse`, `urlContext`, `fileSearch`,
      `mcpServers`, `googleMaps`; represent `Part.toolCall`,
      `Part.toolResponse`, `executableCode`, `codeExecutionResult`.
- [ ] **Media.** Files API and `gs://` URIs in `fileData` (rejected);
      `videoMetadata`, per-part `mediaResolution`, `partMetadata`.
- [ ] **Response metadata.** `citationMetadata`, `groundingAttributions`,
      `groundingMetadata`, `avgLogprobs`, `logprobsResult`,
      `urlContextMetadata`, `modelStatus`; `usageMetadata` modality details,
      tool-use prompt tokens, `serviceTier`.
- [ ] **Usage-only chunks.** Forward them where the source protocol has them.
- [ ] **countTokens.** Use the backend tokenizer where available; count
      function-response media as media (text today,
      `pkg/tokens/estimate.go`); return `cachedContentTokenCount` and
      `cacheTokensDetails`.
- [ ] **Endpoints.** `models.list` / `models.get`, `embedContent`,
      `batchEmbedContents`, the `/v1` prefix, `tunedModels/*` / `dynamic/*`;
      `fields`, `prettyPrint`, `$.xgafv`, `alt=proto`.

### Image generation

- [ ] **Serving.** `responseModalities: [IMAGE]` plus `imageConfig` on
      `generateContent` is not routed to a renderer; image models are only
      reachable through the OpenAI images endpoints and `/v1/render`.
- [ ] **Output format.** `Format` (png / jpeg / webp) is ignored; the
      Interactions API takes `response_format.image.mime_type`, the legacy
      `responseFormat.image.mimeType` is JPEG-only and not in the Go SDK's
      `GenerateContentConfig`.
- [ ] **Grounding and thinking.** Google Search grounding
      (`google_search`, `image_search`) and `thinking_level` for image
      models are not exposed.
- [ ] **Interleaved output.** Only the first image part is returned; text
      parts and further images are dropped.
- [ ] **Reference limits.** Up to 14 input images (3 on 2.5 Flash Image,
      object / character / style roles are prompt-only) are not checked
      before the request.
- [ ] **Mask / markup.** No native field exists; both are emulated
      (`provider.EmulateEditGuides`). Revisit if Gemini adds one.

## Tests

- [ ] Stop ignoring `finishReason`, `finishMessage`, `safetyRatings`,
      `promptFeedback`, `modelVersion`, token-detail fields and `serviceTier`
      in `test/gemini/rules.go` once they are emitted.
- [ ] Conformance cases for built-in tools, multiple candidates, logprobs,
      cached content, and prompt blocking.
