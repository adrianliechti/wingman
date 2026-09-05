# Gemini GenerateContent API compliance TODO

Last review: 2026-09-05 (`1313d4002804`, working tree)

Open items for `models/{model}:generateContent`, `:streamGenerateContent`,
and `:countTokens` against the Gemini API reference and the v1beta discovery
document. Each item is a gap; nothing that already works is listed.
Priorities: **P0** wrong answer or silent 200, **P1** gap on an otherwise
supported path, **P2** missing feature.

## Request decoding and validation

- [ ] P0 Honor or reject `safetySettings` (ignored today).
- [ ] P0 Reject unknown fields and trailing JSON.
- [ ] P0 Reject a tool object that contains only unsupported built-in tools
      instead of decoding it to an empty tool.
- [ ] P1 Validate `contents` presence, `Content.role` values (coerced to
      `user` today), single-field `Part` unions, enum values, numeric ranges,
      name constraints, and schema mutual exclusion.
- [ ] P1 Accept Google API-key authentication (`key=` query,
      `x-goog-api-key`) when an authorizer is configured.
- [ ] P2 Return a Google error body on authentication failure (bare 401
      today).

## Request fields

- [ ] P2 Honor or reject `cachedContent`, `serviceTier`, `store`.

## Generation config

- [ ] P1 Carry `topP`, `topK`, `seed`, `presencePenalty`, `frequencyPenalty`
      to the provider or reject them.
- [ ] P1 Honor `candidateCount` or reject values other than 1.
- [ ] P1 Reject MIME types other than JSON (`text/x.enum` and others are
      ignored).
- [ ] P2 Honor or reject `responseLogprobs`, `logprobs`,
      `responseModalities`, `mediaResolution`.
- [ ] P2 Add `speechConfig`, `imageConfig`, `audioTranscriptionConfig`,
      `enableEnhancedCivicAnswers`, `enableAffectiveDialog`,
      `responseFormat`, `translationConfig`.

## Thinking

- [ ] P1 Preserve the exact `thinkingBudget` for Gemini backends instead of a
      coarse level.
- [ ] P1 Honor `thinkingBudget: 0` on its own as disabled thinking.
- [ ] P1 Validate level / budget combinations instead of letting the level
      win silently.

## Function calling

- [ ] P1 Surface `MALFORMED_FUNCTION_CALL` instead of replacing unparseable
      arguments with `{}`.
- [ ] P1 Keep the `parameters` (OpenAPI) and `parametersJsonSchema` dialects
      distinct.
- [ ] P2 Support `FunctionDeclaration.response`, `responseJsonSchema`,
      `behavior`; `FunctionResponse.scheduling`, `willContinue`;
      `ToolConfig.retrievalConfig`, `includeServerSideToolInvocations`.

## Built-in tools

- [ ] P2 Support or explicitly reject `codeExecution`, `googleSearch`,
      `googleSearchRetrieval`, `computerUse`, `urlContext`, `fileSearch`,
      `mcpServers`, `googleMaps`.
- [ ] P2 Represent `Part.toolCall`, `Part.toolResponse`, `executableCode`,
      `codeExecutionResult`.

## Parts and media

- [ ] P1 Emit generated `inlineData` / `fileData` output parts (dropped
      today).
- [ ] P1 Handle `functionResponse.parts[].fileData` URIs (stored as bytes
      today).
- [ ] P2 Support Files API and `gs://` URIs in `fileData` (rejected today).
- [ ] P2 Add `videoMetadata`, per-part `mediaResolution`, `partMetadata`.

## Response object

- [ ] P0 Return `promptFeedback` with no candidates for a blocked prompt
      instead of an empty candidate.
- [ ] P1 Preserve the exact upstream `finishReason` (reduced to `STOP`,
      `MAX_TOKENS`, `SAFETY`, `OTHER` today).
- [ ] P1 Preserve the upstream `responseId` and `modelVersion`.
- [ ] P1 Emit `safetyRatings`, `finishMessage`, `tokenCount`.
- [ ] P2 Emit `citationMetadata`, `groundingAttributions`,
      `groundingMetadata`, `avgLogprobs`, `logprobsResult`,
      `urlContextMetadata`, `modelStatus`.
- [ ] P2 Emit `usageMetadata` modality details, tool-use prompt tokens, and
      `serviceTier`.

## Streaming

- [ ] P1 Define mid-stream failure behavior separately from the successful
      response schema (a `GenerateContentResponse.error` object is emitted
      today).
- [ ] P1 Close the JSON-array framing with `]` after a mid-stream error.
- [ ] P2 Forward usage-only chunks where the source protocol returns them.

## countTokens

- [ ] P2 Use the backend's tokenizer where available instead of the
      estimate; count function-response media and cached content.
- [ ] P2 Return `cachedContentTokenCount` and `cacheTokensDetails`.

## Endpoints

- [ ] P2 Route `tunedModels/*` and `dynamic/*` resources.
- [ ] P2 Implement `fields`, `prettyPrint`, `$.xgafv`, `alt=proto`.

## Tests to add

- [ ] Stop ignoring `finishReason`, `finishMessage`, `safetyRatings`,
      `promptFeedback`, `modelVersion`, token-detail fields and `serviceTier`
      in `test/gemini/rules.go` once they are emitted.
- [ ] Conformance cases for built-in tools, multiple candidates, logprobs,
      cached content, and prompt blocking.
