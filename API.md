# OpenAI Compatible API

Temperature, top-p, and top-k controls are unsupported across all endpoints; models use their default sampling behavior.

OpenAI-compatible endpoints. See [OpenAI API Reference](https://platform.openai.com/docs/api-reference) for full documentation.

All API routes, including Anthropic, Gemini, and native routes, accept request
compression through `Content-Encoding: gzip`, `x-gzip`, or `zstd`, for both JSON
and multipart bodies. Only one encoding layer is accepted; unsupported or stacked
encodings return `415`. The zstd window is limited to 8 MiB. Requests are decoded
as a stream, preserving support for larger uploads.

## Models

**Endpoints:** `GET /v1/models`, `GET /v1/models/{id}`

List available models or get a specific model by ID.

## Chat Completions

**Endpoint:** `POST /v1/chat/completions`

| Parameter                | Type         | Description                                         |
|--------------------------|--------------|-----------------------------------------------------|
| `model`                  | String       | Model ID                                            |
| `messages`               | Array        | Conversation messages                               |
| `stream`                 | Boolean      | Enable streaming                                    |
| `stream_options`         | Object       | Streaming options (`include_usage`)                 |
| `max_completion_tokens`  | Integer      | Maximum tokens to generate                          |
| `stop`                   | String/Array | Stop sequences                                      |
| `tools`                  | Array        | Available tools/functions                           |
| `tool_choice`            | String/Object| Tool selection mode (auto, none, required)          |
| `parallel_tool_calls`    | Boolean      | Enable parallel tool execution                      |
| `response_format`        | Object       | Response format (text, json_object, json_schema)    |
| `reasoning_effort`       | String       | Reasoning effort (none, minimal, low, medium, high, xhigh, max) |
| `verbosity`              | String       | Output verbosity (low, medium, high)                |
| `prompt_cache_key`       | String       | Groups requests sharing a prefix; forwarded to backends that route caches by key |
| `prompt_cache_retention` | String       | `24h` asks for extended cache retention where the backend offers it |
| `prompt_cache_options`   | Object       | `mode` `implicit` (default) or `explicit`; explicit caches only at `prompt_cache_breakpoint` parts |

Prompt caching is on by default on every backend that supports it: the stable prefix (instructions, tools, earlier turns) is cached automatically, as on OpenAI, and cached tokens are reported in the usage details. A `prompt_cache_breakpoint` on a content part marks the end of a reusable prefix: GPT-5.6 and later take it natively, Claude backends receive it as a cache breakpoint, and other backends keep caching implicitly.

## Responses

**Endpoint:** `POST /v1/responses`

| Parameter              | Type         | Description                                         |
|------------------------|--------------|-----------------------------------------------------|
| `model`                | String       | Model ID                                            |
| `input`                | String/Array | Input text or conversation messages                 |
| `stream`               | Boolean      | Enable streaming                                    |
| `service_tier`         | String       | Best-effort processing tier: `auto`, `default`, `flex`, or `priority` |
| `max_output_tokens`    | Integer      | Maximum tokens to generate                          |
| `tools`                | Array        | Available tools/functions                           |
| `tool_choice`          | String/Object| Tool selection mode (auto, none, required)          |
| `parallel_tool_calls`  | Boolean      | Enable parallel tool execution                      |
| `instructions`         | String       | System instructions                                 |
| `reasoning`            | Object       | Reasoning configuration (`effort`, `summary`)       |
| `text`                 | Object       | Text format and verbosity configuration             |
| `context_management`   | Array        | Context management (compaction with threshold)      |
| `include`              | Array        | Include options (e.g. `reasoning.encrypted_content`)|
| `truncation`           | String       | Truncation mode (auto, disabled)                    |
| `prompt_cache_key`     | String       | Groups requests sharing a prefix; forwarded to backends that route caches by key |
| `prompt_cache_retention` | String     | `24h` asks for extended cache retention where the backend offers it |
| `prompt_cache_options` | Object       | `mode` `implicit` (default) or `explicit`; explicit caches only at `prompt_cache_breakpoint` parts |

Image content parts accept `detail`: `auto`, `low`, `high`, or `original`. Responses preserves this preference for user images and images in function or custom tool outputs when forwarding to an OpenAI Responses backend. Omission keeps the backend default.

Prompt caching is on by default on every backend that supports it: the stable prefix is cached automatically, as on OpenAI, and cached tokens are reported in `usage.input_tokens_details.cached_tokens`. A `prompt_cache_breakpoint` on an input content part marks the end of a reusable prefix: GPT-5.6 and later take it natively, Claude backends receive it as a cache breakpoint, and other backends keep caching implicitly.

Processing tier preferences use `Tier` internally and `service_tier` on the OpenAI-compatible wire. `auto`, omission, and unknown values keep the backend's default and share the empty internal value. `default` explicitly requests standard processing. Known values map as follows; unsupported preferences are omitted:

| Provider | `auto` | `default` | `flex` | `priority` |
|----------|--------|-----------|--------|------------|
| OpenAI / Azure OpenAI | Omit | `default` | `flex` | `priority` |
| Gemini Interactions | Omit | `standard` | `flex` | `priority` |
| Bedrock Converse | Omit | `default` | `flex` | `priority` |
| Claude direct | Omit | `speed: standard` | Omit | `speed: fast` |
| xAI | Omit | `default` | Omit | `priority` |
| Other OpenAI-compatible endpoints | Pass through | Pass through | Pass through | Pass through |

Availability still depends on the model, account, and region. OpenAI-compatible endpoints that explicitly reject `service_tier` with HTTP 400/422 are retried once without it, before any output is delivered. OpenAI-compatible capacity errors such as HTTP 429 are returned normally. Mistral's native selectors are `auto` and `standard_only`; through the generic adapter, rejected common values fall back to omission. OpenRouter supports Flex routing and priority routing with its own fallback rules.

Claude's mapping uses fast inference rather than its legacy Priority Tier capacity. `usage.speed: fast` maps back to `priority`, and `standard` maps to `default`. Unsupported fast mode (HTTP 400/422 referring to speed/fast) or unavailable fast capacity (HTTP 429/529) triggers one retry at standard speed before any completion is delivered. Bedrock uses AWS's native processing tiers separately. See [Claude fast mode](https://platform.claude.com/docs/en/build-with-claude/fast-mode).

The response's `service_tier` reports the provider's effective tier, including downgrades. Native `standard` is returned as `default`. It is `null` before the provider reports it or when that metadata is unavailable; the requested preference is never used as proof of the tier actually served.

References (checked 2026-10-16): [OpenAI Flex](https://developers.openai.com/api/docs/guides/flex-processing), [OpenAI Priority/Fast](https://developers.openai.com/api/docs/guides/fast-mode), [Azure](https://learn.microsoft.com/en-us/azure/foundry/openai/how-to/flex-processing), [Gemini Flex](https://ai.google.dev/gemini-api/docs/flex-inference?hl=en), [Gemini Priority](https://ai.google.dev/gemini-api/docs/priority-inference), [Bedrock](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ServiceTier.html), [Claude](https://platform.claude.com/docs/en/api/service-tiers), [xAI](https://docs.x.ai/developers/advanced-api-usage/priority-processing), [OpenRouter](https://openrouter.ai/docs/guides/features/service-tiers), [Mistral](https://docs.mistral.ai/inference/priority-tier).

## Decisions

**Endpoint:** `POST /v1/decisions`

Evaluate shared text or inline images with ordered questions, following the
[OpenAI Decisions format](https://developers.openai.com/api/docs/guides/decisions).
The supported request fields are `model`, `input`, `questions`,
`prompt_cache_key`, and `prompt_cache_options`.
`model` is optional: Wingman selects the first native decider in provider/model
configuration order, or the first configured decision adapter alias when no
native decider exists. Requests fail if neither is configured.

| Question type | Required fields | Result |
|---------------|-----------------|--------|
| `predicate` / `binary` | `instructions` | `probability` that the condition is true |
| `choice` | `instructions`, `choices` containing string or boolean `value` and optional `description` | `choice`, `probabilities`, `confidence` |
| `score` | `instructions`, ordered `levels` containing `label` (or integer `value`) and optional `description` | `score`, `probabilities`, `confidence` |

Questions may include an `id` or `name`. Answers preserve question order, `id`,
and `name`; unnamed answers use `name: null`. A question may return `type: refusal` or `type: error`.
`input` accepts a string or messages (`user`, `assistant`, `developer`, `system`)
and tool call/output items with `input_text`, `output_text`, `refusal`, `input_json`,
and `input_image` parts. Question `instructions` also accept a string or an array
of `input_text`, `input_json`, and `input_image` parts. Images require base64 data
URLs or publicly accessible HTTP(S) URLs, with at most 128 images across shared
input and question instructions, and at most 400 questions per request.
`input_json` accepts an object or array of objects; nested keys remain ordinary data.

Prompt caching supports `prompt_cache_key`, `prompt_cache_options` (`mode`: `implicit`
or `explicit`, `ttl`: `30m` or `12h`), and `prompt_cache_breakpoint: {"mode":"explicit"}`
on shared text and image parts. Question instructions cannot contain cache
breakpoints. Usage includes cache reads and writes within the input token total.

Configure a native OpenAI decision model:

```yaml
providers:
  - type: openai
    token: ${OPENAI_API_KEY}
    models:
      decisions:
        id: gpt-6-luna
        type: decider
```

```bash
curl http://localhost:8080/v1/decisions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "decisions",
    "input": "The package arrived with a broken screen.",
    "questions": [{
      "type": "predicate",
      "name": "damaged",
      "instructions": "Does the customer report a damaged item?"
    }]
  }'
```

Responses contain `model`, an ordered `answers` array, and `usage`.
The native provider also works through `/v1/systemone`, mapping `noul` to
`predicate` and serializing structured state and criteria as text.
Completion and embedding models can serve text decisions when explicitly enabled
with a `deciders` alias. The alias may reuse the completion or embedding model's
name, keeping its existing API available:

```yaml
providers:
  - type: openai
    token: ${OPENAI_API_KEY}
    models:
      - gpt-6-luna
      - text-embedding-3-small

deciders:
  gpt-6-luna:
    model: gpt-6-luna
    effort: low
  embedding-decisions:
    model: text-embedding-3-small
```

Here `gpt-6-luna` supports completions and decisions, while
`embedding-decisions` enables decisions backed by `text-embedding-3-small`.
Other completion and embedding models remain unavailable for decisions unless
they have a `deciders` entry. Access policies check the selected decision alias,
including when `model` is omitted.

Completion-backed aliases accept optional `effort`: `none`, `minimal`, `low`,
`medium`, `high`, `xhigh`, or `max`. Omitting it or setting `none` keeps the
adapter's disabled reasoning request; provider compatibility rules may select a
minimum effort for models that require reasoning. Other values enable adaptive
reasoning at that effort, subject to the underlying provider's model support.
The strict probability schema always applies, and verbosity is left unset.
Embedding-backed aliases do not accept effort.

Run live native and cross-model checks with `go test -v ./test/openai/decisions`.
The native test compares an in-process Wingman server with direct OpenAI.
The cross-model test uses a running Wingman server and defaults to `gpt-6-luna`;
set `TEST_DECISIONS_MODELS` to a comma-separated list of enabled decision aliases
or native deciders to test other models. Each adapted model needs a `deciders`
entry. `TEST_DECISIONS_NATIVE_MODEL` overrides the native reference model.
To start an isolated server instead, append
`-args -decisions-config /path/to/config.yaml` to the test command.

## Embeddings

**Endpoint:** `POST /v1/embeddings`

| Parameter          | Type         | Description                     |
|--------------------|------------- |---------------------------------|
| `model`            | String       | Model ID                        |
| `input`            | String/Array | Text(s) to embed                |
| `encoding_format`  | String       | Encoding format (float, base64) |
| `dimensions`       | Integer      | Output dimensions               |

## Audio Speech (TTS)

**Endpoint:** `POST /v1/audio/speech`

| Parameter          | Type   | Description          |
|--------------------|--------|----------------------|
| `model`            | String | Model ID             |
| `input`            | String | Text to synthesize   |
| `voice`            | String | Voice ID             |
| `speed`            | Float  | Playback speed       |
| `response_format`  | String | Audio format         |
| `instructions`     | String | Voice instructions   |

## Audio Transcriptions

**Endpoint:** `POST /v1/audio/transcriptions`

| Parameter   | Type   | Description                                      |
|-------------|--------|--------------------------------------------------|
| `model`     | String | Model ID                                         |
| `file`      | File   | Audio file                                       |
| `language`  | String | Audio language                                   |
| `prompt`    | String | Optional text to guide transcription style/vocab |

## Image Generation

**Endpoint:** `POST /v1/images/generations`

| Parameter          | Type   | Description                                            |
|--------------------|--------|-------------------------------------------------------|
| `model`            | String | Model ID                                              |
| `prompt`           | String | Image description                                     |
| `size`             | String | Pixel size (e.g. `1024x1536`) or `auto`               |
| `aspect_ratio`     | String | Aspect ratio (e.g. `1:1`, `16:9`, `1:4`); overrides `size` |
| `quality`          | String | `low`, `medium`, `high`, `xhigh`, `max` (`standard`/`hd` accepted) |
| `resolution`       | String | `512`, `1K`, `2K`, `4K`                                |
| `background`       | String | `transparent` or `opaque`                             |
| `output_format`    | String | `png`, `jpeg`, or `webp`                              |
| `response_format`  | String | url or b64_json                                       |

Geometry and quality are provider-neutral: `size`/`aspect_ratio`/`quality`/`resolution` are mapped to each model's supported values (aspect ratios snap to the nearest available, unsupported options are dropped), so the same request works across providers. The extreme ratios `1:4`, `4:1`, `1:8` and `8:1` are native on Nano Banana 2.1 and Gemini 3.1 Flash Image; other models snap to their nearest ratio (GPT Image 2.5 clamps to 1:3 / 3:1).

## Image Edit

**Endpoint:** `POST /v1/images/edits`

| Parameter          | Type   | Description                                            |
|--------------------|--------|-------------------------------------------------------|
| `model`            | String | Model ID                                              |
| `prompt`           | String | Edit instructions                                    |
| `image`            | File   | Image(s) to edit; `image[]` for several              |
| `mask`             | File   | Optional alpha mask: transparent pixels mark where the first image may change |
| `markup`           | File   | Optional transparent PNG with annotations (circles, arrows, notes) drawn over the first image |
| `size`             | String | Pixel size (e.g. `1024x1536`) or `auto`               |
| `aspect_ratio`     | String | Aspect ratio (e.g. `1:1`, `16:9`); overrides `size`   |
| `quality`          | String | `low`, `medium`, `high`, `xhigh`, `max` (`standard`/`hd` accepted) |
| `resolution`       | String | `512`, `1K`, `2K`, `4K`                                |
| `background`       | String | `transparent` or `opaque`                             |
| `output_format`    | String | `png`, `jpeg`, or `webp`                              |
| `response_format`  | String | url or b64_json                                       |

`mask` and `markup` must be PNG files matching the first image's dimensions. Only fully transparent mask pixels (alpha = 0) mark editable regions. OpenAI sends masks natively. Google and OpenRouter emulate masks with a black-and-white reference and prompt instructions; adherence is best effort. xAI rejects masks because this adapter sends only one edit image. Markup is composited onto the first image; when combined with a mask, it is sent as a separate annotated reference so the original stays intact. Native OpenAI markup remains disabled after the endpoint rejected the field on 2026-11-04.

---

# Anthropic Compatible API

Anthropic-compatible endpoints. See [Anthropic API Reference](https://docs.anthropic.com/en/api/messages) for full documentation.

## Messages

**Endpoint:** `POST /v1/messages`

| Parameter             | Type         | Description                                    |
|-----------------------|--------------|------------------------------------------------|
| `model`               | String       | Model ID                                       |
| `messages`            | Array        | Conversation messages                          |
| `system`              | String/Array | System prompt                                  |
| `max_tokens`          | Integer      | Maximum tokens to generate                     |
| `stream`              | Boolean      | Enable streaming                               |
| `stop_sequences`      | Array        | Stop sequences                                 |
| `tools`               | Array        | Available tools/functions                      |
| `tool_choice`         | Object       | Tool selection (auto, any, tool, none)         |
| `output_format`       | Object       | Structured output with JSON schema             |
| `output_config`       | Object       | Structured output and reasoning effort         |
| `thinking`            | Object       | Thinking configuration (type, budget_tokens)   |
| `speed`               | String       | `standard` maps to `TierDefault`; `fast` maps to `TierPriority`, with best-effort backend support |
| `context_management`  | Object       | Context management with compaction edits       |
| `compaction`          | Object       | `{"type":"summarize"}` requests on-demand Claude compaction |
| `cache_control`       | Object       | Breakpoints on system and message blocks; the prefix is cached by default on Claude backends, GPT-5.6 and later receive them as `prompt_cache_breakpoint`, and `ttl: "1h"` asks for extended retention |

## Count Tokens

**Endpoint:** `POST /v1/messages/count_tokens`

| Parameter   | Type         | Description           |
|-------------|------------- |-----------------------|
| `model`     | String       | Model ID              |
| `messages`  | Array        | Conversation messages |
| `system`    | String/Array | System prompt         |
| `tools`     | Array        | Tool definitions      |

---

# Gemini Compatible API

Gemini-compatible endpoints. See [Gemini API Reference](https://ai.google.dev/api/generate-content) for full documentation.

## Gemini Decisions

**Endpoint:** `POST /v1beta/decisions`

Uses the [Gemini Decisions format](https://aistudio.google.com/docs/decisions-api).
Configure `gemini-decision-1` under a `gemini` or `google` provider. The model is
automatically registered as a native decider, available on all three decision
endpoints. As on the other decision endpoints, omitting `model` selects the
first configured native decider, then the first enabled adapter alias.

| Field | Type | Description |
|---|---|---|
| `model` | String | Configured decision model or alias; optional in Wingman |
| `input` | String, content object, or array | Shared text, image, audio, video, or PDF evidence |
| `questions` | Object | 1–1,000 named questions; keys use 1–64 ASCII letters, digits, underscores, or hyphens |

Each question requires `type` and `instructions`. Instructions accept a string,
one `{"type":"text","text":"..."}` block, or an array of text blocks.
Adjacent text blocks concatenate without an added separator.

| Type | `criteria` | Answer |
|---|---|---|
| `noul` | Optional object with `true_description` and/or `false_description` strings | `noul` probability and `confidence` |
| `choice` | 2–255 option IDs mapped to string descriptions (empty descriptions allowed) | `choice`, `probabilities`, `confidence` |
| `score` | 2–255 nonempty strings, ordered from level 0 | Weighted `score`, per-level `probabilities`, `legend`, `confidence` |

Media blocks require `type`, `mime_type`, and exactly one of base64 `data` or
`uri`. URI references are forwarded to Google without being downloaded by
Wingman. Use a Files API URI or HTTPS URL; YouTube and `gs://` inputs are rejected.
Image and video blocks accept `resolution`: `low`, `medium`, or `high`;
images also accept `ultra_high`.

```bash
curl http://localhost:4242/v1beta/decisions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gemini-decision-1",
    "input": "The package arrived with a cracked screen.",
    "questions": {
      "is_damaged": {"type": "noul", "instructions": "Did the item arrive damaged?"},
      "next_action": {
        "type": "choice", "instructions": "Which action fits?",
        "criteria": {"replace": "Damaged product.", "close": "No issue reported."}
      },
      "severity": {
        "type": "score", "instructions": "Rate severity.",
        "criteria": ["Cosmetic", "Partly usable", "Unusable"]
      }
    }
  }'
```

Responses contain `answers` keyed by the question IDs and `usage` with
`total_input_tokens`, `total_output_tokens`, and `total_tokens`. The native Gemini
backend bills no output tokens. Wingman reports aggregate usage and omits the
optional per-modality breakdown. Completion and embedding adapters can serve this
endpoint when explicitly enabled in `deciders`; their probabilities and confidence
remain estimates and their output token usage is reported normally.

Google's backend rejects conversation history, tool/thought blocks, box questions,
and media in question instructions or criteria. The OpenAI and System One
endpoints retain their own request limits, including their 10-level rubric limit.
Model access policies, budget admission, and telemetry apply through the shared
decider interface.

## Generate Content

**Endpoint:** `POST /v1beta/models/{model}:generateContent`

| Parameter            | Type   | Description                                  |
|----------------------|--------|----------------------------------------------|
| `contents`           | Array  | Conversation contents with parts             |
| `systemInstruction`  | Object | System instruction content                   |
| `tools`              | Array  | Available tools/functions                    |
| `toolConfig`         | Object | Tool configuration (mode: AUTO, ANY, NONE)   |
| `generationConfig`   | Object | Generation parameters                        |
| `safetySettings`     | Array  | Safety filter settings                       |
| `serviceTier`        | String | Best-effort processing tier: `standard`, `flex`, or `priority`; `service_tier` spelling is also accepted |

The tier is a top-level request field for both `generateContent` and `streamGenerateContent`. Native `standard` maps to the shared `TierDefault`; unknown values keep the backend's default. When available, the effective tier is returned as `usageMetadata.serviceTier` and as `X-Gemini-Service-Tier` if known before response headers are sent. See the [Gemini Flex guide](https://ai.google.dev/gemini-api/docs/generate-content/flex-inference).

**Generation Config:**

| Parameter            | Type    | Description                                 |
|----------------------|---------|---------------------------------------------|
| `stopSequences`      | Array   | Stop sequences                              |
| `maxOutputTokens`    | Integer | Maximum tokens to generate                  |
| `candidateCount`     | Integer | Number of candidates to generate            |
| `responseMimeType`   | String  | Response MIME type                          |
| `responseSchema`     | Object  | JSON schema for structured output           |
| `responseJsonSchema` | Object  | JSON schema (alternative format)            |
| `thinkingConfig`     | Object  | Thinking configuration (level, budget, includeThoughts) |

## Stream Generate Content

**Endpoint:** `POST /v1beta/models/{model}:streamGenerateContent`

Same parameters as Generate Content. Returns Server-Sent Events when `?alt=sse` query parameter is provided, otherwise returns a JSON array.

## Count Tokens

**Endpoint:** `POST /v1beta/models/{model}:countTokens`

| Parameter            | Type   | Description                       |
|----------------------|--------|-----------------------------------|
| `contents`           | Array  | Conversation contents with parts  |
| `systemInstruction`  | Object | System instruction content        |
| `tools`              | Array  | Tool definitions                  |

---

# Utility APIs

## Extract

Extract text from files or URLs.

**Endpoint:** `POST /v1/extract`

| Parameter   | Type   | Description                             |
|-------------|--------|-----------------------------------------|
| `model`     | String | Model/provider to use                   |
| `file`      | File   | Document to extract text from           |
| `url`       | String | URL to scrape and extract               |
| `schema`    | JSON   | JSON schema for structured extraction   |

**Headers:**

| Header    | Value                | Description                               |
|-----------|----------------------|-------------------------------------------|
| `Accept`  | `application/json`   | Structured output with OCR details        |

```bash
# From file (text output)
curl -X POST -F "file=@document.pdf" http://localhost:8080/v1/extract

# From URL (text output)
curl -X POST -F "url=https://example.com" http://localhost:8080/v1/extract

# JSON output with OCR details (pages, blocks, polygons)
curl -X POST -H "Accept: application/json" -F "file=@document.pdf" http://localhost:8080/v1/extract

# With JSON schema for structured extraction
curl -X POST -F "file=@document.pdf" -F 'schema={"type":"object","properties":{"name":{"type":"string"}}}' http://localhost:8080/v1/extract
```

## Render

Generate images from text descriptions.

**Endpoint:** `POST /v1/render`

| Parameter       | Type     | Description                                          |
|-----------------|----------|-----------------------------------------------------|
| `model`         | String   | Model/provider to use                               |
| `input`         | String   | Text description to render                          |
| `file`          | File(s)  | Optional reference images                           |
| `mask`          | File     | Optional alpha mask for the first image (see [Image Edit](#image-edit)) |
| `markup`        | File     | Optional annotated overlay for the first image (see [Image Edit](#image-edit)) |
| `size`          | String   | Pixel size (e.g. `1024x1536`) or `auto`             |
| `aspect_ratio`  | String   | Aspect ratio (e.g. `1:1`, `16:9`); overrides `size` |
| `quality`       | String   | `low`, `medium`, `high`, `xhigh`, `max`             |
| `resolution`    | String   | `512`, `1K`, `2K`, `4K`                              |
| `background`    | String   | `transparent` or `opaque`                           |

Returns the raw image bytes. The output format is negotiated via the `Accept` header (`image/png`, `image/jpeg`, `image/webp`). Geometry and quality options are mapped to each model's supported values (see [Image Generation](#image-generation)).

```bash
curl -X POST -F "input=a sunset over mountains" -F "aspect_ratio=16:9" \
  -H "Accept: image/webp" http://localhost:8080/v1/render
```

## Search

Search for information.

**Endpoint:** `POST /v1/search` (alias: `/v1/retrieve`)

| Parameter   | Type    | Description            |
|-------------|---------|------------------------|
| `model`     | String  | Model/provider to use  |
| `query`     | String  | Search query; `input` is also accepted |
| `limit`     | Integer | Maximum results        |
| `text` | Object or Boolean | `{}` or true requests page text; omitted, null or false returns excerpts only |
| `category`  | String  | Provider vertical (e.g. `news`, `publication`); see provider categories |
| `location`  | String  | ISO 3166-1 alpha-2 country code to bias results |
| `domain`    | String  | Repeatable; restrict to a domain, or exclude one with a `!` prefix |
| `since`     | String  | Earliest publication date (RFC 3339 or `YYYY-MM-DD`) |
| `until`     | String  | Latest publication date; `YYYY-MM-DD` includes the whole UTC day, RFC 3339 is an exclusive timestamp bound |

```bash
curl -X POST -H 'Content-Type: application/json' \
  -d '{"query":"your query","category":"news","text":{}}' \
  http://localhost:8080/v1/search
```

JSON, URL-encoded forms and multipart forms are supported. In forms, pass
`text={}` or `text=true`; repeat `domain` for domain filters. JSON uses a
`domain` array. `text` is reserved for retrieval options and is no longer a
query alias. Unsupported text options return HTTP 400.

Search options vary by provider. Unsupported filters return an error rather than
being silently ignored.

| Provider | Categories | Location | Date filters | Result limit |
|----------|------------|----------|--------------|--------------|
| Exa | `company`, `people`, `news`, `publication`, `personal site`, `financial report`; other strings are hints. Legacy `research paper` maps to `publication`. | ISO country code | Supported except with `company` or `people`; only `people` rejects domain exclusions | 1–100 |
| Tavily | `general` (default), `news`, `finance`; legacy `financial report` maps to `finance` | Supported country code, translated to Tavily's country name; `general` only | Strict filtering by estimated publication/update dates; undated results are omitted when bounds are supplied | 0–20 |
| DuckDuckGo | General web search only | Supported country code, translated to a documented region/locale | Not supported by the HTML adapter | Positive integer |
| Custom | Forwarded to the gRPC provider | Forwarded to the gRPC provider | When advertised by the gRPC provider | Positive int32 |

Exa supports up to 1200 included and 1200 excluded domains. Tavily supports up
to 300 included and 150 excluded domains. DuckDuckGo accepts hostnames, searches
each included domain separately, combines the results, and enforces exclusions.
Publication dates may be unavailable; Tavily dates are estimates of publication
or the last update. Exa and Tavily preserve explicit timestamp bounds, with
Tavily applying timestamp filtering after its calendar-date search.

Each result carries a source, title, and a query-relevant `excerpt`
when the provider has evidence available. `text={}` or `text=true` also returns
`text` for successfully retrieved pages. Exa and Tavily retrieve that text in
the search call; other providers use the configured scraper. Failed page
retrieval preserves the result and its excerpt, with `text` omitted.

Provider references: [Exa Search](https://exa.ai/docs/reference/search),
[Tavily Search](https://docs.tavily.com/documentation/api-reference/endpoint/search),
[DuckDuckGo parameters](https://duckduckgo.com/duckduckgo-help-pages/settings/params),
[DuckDuckGo search syntax](https://duckduckgo.com/duckduckgo-help-pages/results/syntax),
and [custom searcher protocol](pkg/searcher/custom/searcher.proto).

## Web retrieval compatibility

Tavily and Exa clients can point their API base URLs at Wingman. These are
compatibility subsets over the same provider-neutral search and scraping
interfaces used by the native endpoints. The client API does not select an
upstream vendor: configure `searchers` and `scrapers` independently.

| Client API | Base URL | Endpoints |
| --- | --- | --- |
| Tavily | `http://localhost:8080/v1/tavily` | `POST /search`, `POST /extract` |
| Exa | `http://localhost:8080/v1/exa` | `POST /search`, `POST /contents` |

All endpoints require a JSON object and `Content-Type: application/json`.
They use Wingman's configured authorizers and model access policies. Clients
send a **Wingman credential**, not an upstream vendor key. Bearer authentication
works for both; Exa's `x-api-key` and Tavily's legacy JSON `api_key` are also
accepted. An explicit Authorization header takes precedence.

Omitting `model` selects the first configured searcher or scraper. A Wingman
extension, `model` in the JSON body or query string, selects a named backend.
For page contents that the searcher cannot retrieve itself, configure a searcher
and scraper under the same name. The SDK examples below use the configured
defaults, so switching backend requires no client changes.

```python
from tavily import TavilyClient
from exa_py import Exa

tavily = TavilyClient(api_key="WINGMAN_KEY",
                     api_base_url="http://localhost:8080/v1/tavily")
results = tavily.search("Go documentation", max_results=3,
                       include_domains=["go.dev"])
pages = tavily.extract(["https://go.dev/doc/"])

exa = Exa(api_key="WINGMAN_KEY", base_url="http://localhost:8080/v1/exa")
results = exa.search("Go documentation", num_results=3,
                    include_domains=["go.dev"])
pages = exa.get_contents(["https://go.dev/doc/"], text={"max_characters": 1000})
```

```typescript
import { tavily } from '@tavily/core'
import Exa from 'exa-js'

const tvly = tavily({ apiKey: 'WINGMAN_KEY', apiBaseURL: 'http://localhost:8080/v1/tavily' })
await tvly.search('Go documentation', { maxResults: 3 })
await tvly.extract(['https://go.dev/doc/'])

const exa = new Exa('WINGMAN_KEY', 'http://localhost:8080/v1/exa')
await exa.search('Go documentation', { numResults: 3 })
await exa.getContents(['https://go.dev/doc/'], { text: { maxCharacters: 1000 } })
```

### Supported retrieval options

| Capability | Tavily JSON | Exa JSON |
| --- | --- | --- |
| Search query | `query` | `query` |
| Result count | `max_results` (0–20; default 10) | `numResults` (1–20; default 10) |
| Domain filters | `include_domains`, `exclude_domains` | `includeDomains`, `excludeDomains` |
| Category | `topic`: `general`, `news`, `finance` | `category` from the selected provider's capabilities |
| Country bias | `country`: Tavily country name or ISO code; general topic only | `userLocation`: ISO country code string |
| Publication bounds | `start_date`, `end_date`: YYYY-MM-DD; end includes that UTC day | `startPublishedDate`, `endPublishedDate`: RFC3339 or YYYY-MM-DD; end is exclusive |
| Recency | `time_range`: day/week/month/year or d/w/m/y; `days` for news | Use publication bounds |
| URL extraction | `urls`: string or array, 1–20 URLs | `urls` or legacy `ids`: string or array, 1–20 URL identifiers |
| Extraction timeout | `timeout`: 1–60 seconds per URL; default 10 | 30 seconds per URL |
| Search excerpts | Always in `content` | `contents.highlights`: true or `{ "maxCharacters": N }` |
| Page text in search | `include_raw_content`: true, `"markdown"`, or `"text"` | `contents.text`: true or `{ "maxCharacters": N }` |
| Extracted text limit | Unavailable | `text`: true or `{ "maxCharacters": N }` |

Domain filters allow up to 300 included and 150 excluded hostnames. Date and
category filters require support from the selected backend; `finance` and
`financial report` are translated between Tavily and Exa. Recency windows use
1, 7, 30, or 365 days and intersect with explicit publication bounds.

Search and extraction use the backend's configured retrieval strategy.
The compatibility request schemas contain only implemented fields. All other
fields are ignored, including unknown nested content options, so new vendor
parameters do not break existing clients. For example, `livecrawlTimeout`,
`search_depth`, `extract_depth`, and future SDK fields do not cause HTTP 400.
Ignored fields are not forwarded to the backend.

The current [Exa Search](https://exa.ai/docs/reference/search),
[Exa Contents](https://exa.ai/docs/reference/get-contents),
[Tavily Search](https://docs.tavily.com/documentation/api-reference/endpoint/search),
and [Tavily Extract](https://docs.tavily.com/documentation/api-reference/endpoint/extract)
references distinguish several kinds of options:

| Field role | Examples | Compatibility behavior |
| --- | --- | --- |
| Implemented inputs and retrieval controls | Query/URLs, result count, domains, category, location, publication bounds, text/highlights selectors, `maxCharacters`; Tavily extraction `timeout` | Applied and validated. |
| Retrieval tuning | Exa `livecrawlTimeout`, retrieval-only `type` modes, `useAutoprompt`, verbosity/highlight sizing; Tavily depth/chunk settings and language preference | Ignored; backend configuration applies. |
| Additional behavior or output | Exa deep search, answers/summaries, images, streaming, structured output, freshness/snapshots, subpages, strict language or moderation controls | Ignored; the response contains only Wingman's supported retrieval fields. |
| Optional metadata or future fields | User attribution, usage, favicons, newly introduced parameters | Ignored. |

Checked against Exa 3.0 on 2026-10-20 and the official Exa SDKs 2.26.0.
The portable endpoint retains its 20-result and 300/150-hostname limits;
the Exa provider itself supports 100 results and 1200 included/excluded domains.
Exa's domain paths, location objects, `objective`, synthesized
output and streaming are outside this subset. `type: "lite"` and
`contents.highlights.dynamic` are accepted as tuning hints; configured backend
behavior applies, including Dynamic Highlights when using the Exa provider.

This is best-effort compatibility: accepting an unsupported field does not
implement its behavior. Extracted content is the backend's normalized text,
which may contain Markdown. Tavily `include_raw_content: true`, `"markdown"`,
and `"text"` all request that same text; format hints do not trigger conversion.
Malformed JSON and invalid supported fields still return HTTP 400. Unknown
endpoints return HTTP 404. The native `/v1/search` schema remains strict.

Tavily returns `query`, `answer: null`, `images: []`, `results`, `response_time`,
and `request_id`; each result includes `url`, `title`, `content`, and
`raw_content` (null unless page text is requested and available).
`score` is included only when the backend supplies it.
Exa returns `requestId`, `results`, and optional retrieval `statuses` and
`searchTime`. Exa results omit scores. Result IDs are URLs and can be passed
to `/contents`. Full-text searches reuse complete text returned by the searcher;
only sources for which full-text retrieval was not attempted use the configured
scraper. Unavailable search text is reported as a partial failure, without an
automatic fresh crawl. Use `/contents` to explicitly retry a page fetch.
No synthetic relevance scores or vendor credit charges are returned.

Extraction runs at most four fetches concurrently and preserves input order
within its results and failures. A page failure does not fail the whole batch:
Tavily returns `failed_results`; Exa returns one `statuses` entry per URL.
Provider errors use HTTP 502, validation errors use HTTP 400, and access-denied
providers use HTTP 404. Errors follow the selected API's JSON envelope.

### Shared result contract

The current [Exa OpenAPI specification](https://exa.ai/docs/exa-spec.yaml) and
the embedded OpenAPI schemas for [Tavily Search](https://docs.tavily.com/documentation/api-reference/endpoint/search)
and [Tavily Extract](https://docs.tavily.com/documentation/api-reference/endpoint/extract)
separate evidence retrieval from generated answers. The common searcher
interface always returns excerpts and has one optional full-text switch:

```go
type SearchOptions struct {
    // Existing count, category, location, domain and date filters omitted.
    Text *TextOptions
}

// nil Text means excerpts only; &TextOptions{} also requests page text.
type TextOptions struct{}
```

| Requested evidence | Exa mapping | Tavily mapping | Separate fetch |
| --- | --- | --- | --- |
| Query excerpts (always) | `contents.highlights` | `results[].content` | Never |
| Optional page text | `contents.text` | `include_raw_content` | Only when the search backend has no integrated text retrieval |
| Known URL content | `/contents` | `/extract` | Call the configured scraper directly |

There is no excerpt selector or character budget in the shared interface.
Providers use their normal excerpt size. Exa SDK `maxCharacters` is retained
as an optional compatibility response limit (1–1,000,000 Unicode characters),
including for highlights; it does not alter the shared provider request.
`Capabilities.Content` distinguishes integrated page retrieval from a
searcher-plus-scraper fallback. An unavailable page
must retain its retrieval failure rather than silently substituting a snippet
for full text or initiating an unrequested fresh crawl.

Exa SDK 2.26.0 defaults `search()` to requesting text, even though its HTTP
content controls are optional. The official SDK probes explicitly request
both text and dynamic highlights through each backend.
Absent or empty search `contents` does not request text in Wingman. The
`/contents` extraction subset defaults to text retrieval when no text selector
is supplied.
The Exa provider defaults to `auto` and always requests Dynamic Highlights
(`contents.highlights.dynamic: true`). It sends the
`Exa-Beta: dynamic-highlights-2026-08-28` header still required by the published
[Highlights documentation](https://exa.ai/docs/search/highlights) and OpenAPI
schema, although the [Exa 3.0 announcement](https://exa.ai/blog/exa-3) describes
general availability. No separate Wingman configuration switch is needed.
Dynamic selection can leave a redundant result without an excerpt; Wingman
preserves that decision instead of filling it with locally selected text.
`vars.mode: lite` selects cached-only retrieval with lower cost and latency;
legacy `fast` and `instant` modes remain supported. Deep/synthesis modes belong
to research rather than this search provider.

Page text is added only when `SearchOptions.Text` is non-nil. Shared result fields
are `Excerpt` for query evidence and `Text` for page text. Compatibility endpoints expose excerpts only
in the fields requested by their SDK dialect. Custom gRPC services can advertise
`content` and handle the optional `text` options message; older services continue to return
excerpts, with optional text composed through the configured scraper.

Freshness belongs to content retrieval options, separately from output shape.
Exa supports cache preference, a maximum cached age, forced fresh retrieval,
and cached-only retrieval. Tavily's published Search/Extract schemas do not
expose equivalent cache-policy fields, so those controls require explicit
capabilities or a cache in Wingman before they can be advertised as portable.
Similarly, search speed/quality presets belong to provider configuration or a
separate common search profile; provider-specific depth names are not identical
algorithms. Generated answers, summaries, and structured synthesis belong to
research/completion interfaces, with structured citation support.

### Additional API assessment

The current [Exa API reference](https://exa.ai/docs/reference/answer) includes
`/answer`, which combines search, LLM synthesis, and structured source citations.
It is a useful next addition once Wingman's research interface carries citation
data alongside report text; mapping report text alone would lose that contract.
The [Tavily API](https://docs.tavily.com/documentation/api-reference/endpoint/research)
also includes asynchronous research, crawl, and map operations. These require
task lifecycle or site traversal capabilities that the current provider-neutral
interfaces do not expose, so they are outside this retrieval subset.

### Verification

The dedicated `server/tavily` and `server/exa` tests cover authentication,
access policies, filter translation, partial failures, Unicode limits, and
ignored vendor/future fields alongside validation of supported inputs. Live
tests load the nearest `.env` and require both `TAVILY_API_KEY` and `EXA_API_KEY`.
They compare portable response fields
against the native APIs and exercise both API dialects over both hosted backends,
plus extraction through the built-in HTTP fetcher.

```shell
# Live HTTP contracts against both vendors
WEB_LIVE_TEST=1 go test -v ./server/tavily ./server/exa -run TestLiveHTTP

# Official JavaScript and Python SDKs, over both hosted backends
npm ci --prefix test/webtest/sdk
uv venv /tmp/wingman-web-sdk-venv
uv pip install --python /tmp/wingman-web-sdk-venv/bin/python -r test/webtest/sdk/requirements.txt
WEB_LIVE_TEST=1 WEB_SDK_TEST=1 WEB_TEST_PYTHON=/tmp/wingman-web-sdk-venv/bin/python \
  go test -v ./server/tavily ./server/exa -run TestLiveSDK
```

## Research

Deep research on a topic.

**Endpoint:** `POST /v1/research`

| Parameter      | Type   | Description              |
|----------------|--------|--------------------------|
| `model`        | String | Model/provider to use    |
| `instructions` | String | Research topic/question  |

```bash
curl -X POST -F "input=explain quantum computing" http://localhost:8080/v1/research
```

## Rerank

Rerank texts by relevance to a query.

**Endpoint:** `POST /v1/rerank`

| Parameter   | Type    | Description                 |
|-------------|---------|-----------------------------|
| `model`     | String  | Model/provider to use       |
| `query`     | String  | Search query                |
| `texts`     | Array   | List of texts to rerank     |
| `limit`     | Integer | Maximum results to return   |

```bash
curl -X POST -H "Content-Type: application/json" \
  -d '{"query":"search term","texts":["text1","text2"],"limit":5}' \
  http://localhost:8080/v1/rerank
```

## System One

**Endpoint:** `POST /v1/systemone`

Evaluate application state with typed questions using a configured native decision
model or an explicitly enabled completion or embedding adapter. Requests and
responses follow the [TypeSafe System One format](https://docs.typesafe.ai/api/system-one).

| Parameter | Type | Description |
|-----------|------|-------------|
| `model` | String | Optional decision model ID or alias; defaults to the first native decider, then the first `deciders` alias in configuration order |
| `state` | Object, string, array, or null | Required state to evaluate |
| `questions` | Object | Required, nonempty map of question IDs to questions |

Each question has a `type`, optional `instructions`, and `criteria` as required
for its type. Instructions and criterion descriptions may be text, structured
JSON, or null. Supply images in an optional `assets` map, keyed by placeholders
used in `state`, instructions, or criteria. Each asset has `type: "image"`,
`format: "png"`, `"jpg"`, or `"jpeg"`, and `data_base64`. The TypeSafe provider
sets `TypeSafe-Danger-Zone-Images: true` when sending images or box questions.
Multipart requests contain the JSON as the first part named `request` and images
as additional named parts; assets reference these with `part` instead of
`data_base64`. Every image part must be used by exactly one asset.

| Type | Criteria / Options | Answer |
|------|--------------------|--------|
| `noul` (yes/no) | Optional object with `true` and/or `false` descriptions | `noul`: probability of yes |
| `choice` | Object of 1–255 named options with descriptions | `choice`: selected option; `probabilities`, `confidence` |
| `score` | Array of 1–10 ordered level descriptions | `score`: weighted level index; `legend`, `probabilities`, `confidence` |
| `box` | Locate an object in the supplied image asset | `box`: `{top, bottom, left, right}` as fractions from 0 to 1; `confidence`: number or null |

Each answer also includes its `type`.

```bash
curl http://localhost:8080/v1/systemone \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "your-model",
    "state": {"ticket": "I was charged twice. Please refund the extra charge."},
    "questions": {
      "refund": {"type": "noul", "instructions": "Is the customer requesting a refund?"},
      "team": {"type": "choice", "instructions": "Which team should handle this?",
        "criteria": {"billing": "Payments and refunds", "technical": "Product faults"}},
      "urgency": {"type": "score", "instructions": "How urgent is the request?",
        "criteria": ["Routine", "Time-sensitive", "Immediate action required"]}
    }
  }'
```

The response includes `model`, `answers` keyed by question ID, and `usage`
(`input_tokens` and `output_tokens`); `id` is included when available. Choice
selects the highest probability, using alphabetical order for ties. Score is a
zero-based, probability-weighted index: three levels yield a value from 0 to 2,
including fractions. `legend` retains the input level descriptions.

Native `typesafe` providers preserve the upstream answers and usage. Completion
and embedding adapters estimate probabilities and derive confidence from those
estimates. Configure the complete upstream evaluation URL to use TypeSafe,
OpenRouter, or a local System One server; see [TypeSafe configuration](README.md#typesafe--system-one).
Enable adapters with `deciders` entries as shown in [Decisions](#decisions).
An omitted `model` uses the same selection and alias access policy as
`/v1/decisions`; requests fail when no decision model is configured.

## Segment

Split text into segments/chunks.

**Endpoint:** `POST /v1/segment`

| Parameter          | Type    | Description                   |
|--------------------|---------|-------------------------------|
| `text`             | String  | Text to segment               |
| `file`             | File    | File to extract and segment   |
| `url`              | String  | URL to scrape and segment     |
| `segment_length`   | Integer | Target segment length         |
| `segment_overlap`  | Integer | Overlap between segments      |
| `model`            | String  | Model/provider to use         |

```bash
curl -X POST -F "input=long text here" -F "segment_length=500" http://localhost:8080/v1/segment
```

## Summarize

Summarize text content.

**Endpoint:** `POST /v1/summarize`

| Parameter   | Type   | Description                     |
|-------------|--------|---------------------------------|
| `model`     | String | Model/provider to use           |
| `input`     | String | Text to summarize               |
| `file`      | File   | File to extract and summarize   |
| `url`       | String | URL to scrape and summarize     |

```bash
curl -X POST -F "input=long article text" http://localhost:8080/v1/summarize
```

## Translate

Translate text or files to another language.

**Endpoint:** `POST /v1/translate`

| Parameter   | Type   | Description            |
|-------------|--------|------------------------|
| `model`     | String | Model/provider to use  |
| `input`     | String | Text to translate      |
| `file`      | File   | File to translate      |
| `language`  | String | Target language        |

```bash
curl -X POST -F "input=Hello world" -F "language=de" http://localhost:8080/v1/translate
```

## Transcribe

Transcribe audio files to text.

**Endpoint:** `POST /v1/transcribe`

| Parameter      | Type   | Description                                      |
|----------------|--------|--------------------------------------------------|
| `model`        | String | Model/provider to use                            |
| `file`         | File   | Audio file to transcribe                         |
| `language`     | String | Audio language (optional)                        |
| `instructions` | String | Optional text to guide transcription style/vocab |

```bash
curl -X POST -F "file=@audio.mp3" http://localhost:8080/v1/transcribe
```

## MCP Proxy

Proxy requests to configured MCP (Model Context Protocol) servers.

**Endpoint:** `POST /v1/mcp/{id}`, `POST /v1/mcp/{id}/*`

Forwards requests to the MCP server identified by `{id}`.

Built-in MCP servers also use this endpoint. The local configuration combines `web_search`, `web_fetch`, and `web_research` at `/v1/mcp/internet`. Search returns ranked sources and excerpts, fetch reads a source URL, and research accepts `{"instructions":"..."}` and returns a cited report as text.

To request progress, include `_meta.progressToken` (a string or integer) in the `tools/call` parameters. The response stream includes `notifications/progress` with the same token, an increasing `progress` counter, and a human-readable `message`. `total` is omitted because the research workflow is open-ended. No progress notifications are sent without a valid token, or after the tool returns or is cancelled. See the research MCP example in [README.md](README.md).
