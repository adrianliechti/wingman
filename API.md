# OpenAI Compatible API

Temperature, top-p, and top-k controls are unsupported across all endpoints; models use their default sampling behavior.

OpenAI-compatible endpoints. See [OpenAI API Reference](https://platform.openai.com/docs/api-reference) for full documentation.

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

Prompt caching is on by default on every backend that supports it: the stable prefix is cached automatically, as on OpenAI, and cached tokens are reported in `usage.input_tokens_details.cached_tokens`. A `prompt_cache_breakpoint` on an input content part marks the end of a reusable prefix: GPT-5.6 and later take it natively, Claude backends receive it as a cache breakpoint, and other backends keep caching implicitly.

## Decisions

**Endpoint:** `POST /v1/decisions`

Evaluate shared text or inline images with ordered questions, following the
[OpenAI Decisions format](https://developers.openai.com/api/docs/guides/decisions).
The supported request fields are `model`, `input`, and `questions`.

| Question type | Required fields | Result |
|---------------|-----------------|--------|
| `predicate` | `instructions` | `probability` that the condition is true |
| `choice` | `instructions`, `choices` containing string or boolean `value` and optional `description` | `choice`, `probabilities`, `confidence` |
| `score` | `instructions`, ordered `levels` containing `label` and optional `description` | `score`, `probabilities`, `confidence` |

Questions may include a `name`. Answers preserve question order and names;
unnamed answers use `name: null`. A question may return `type: refusal`.
`input` accepts a string or user messages with `input_text` and `input_image`
parts. Images require base64 data URLs, with at most 128 images per request.

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
Existing completion and embedding models can serve text decisions through
the same adapters used by System One.

Run live native and cross-model checks with `go test -v ./test/openai/decisions`.
The native test compares an in-process Wingman server with direct OpenAI.
The cross-model test uses a running Wingman server and the shared GPT, Claude,
and Gemini test model list plus `text-embedding-3-small`; set
`TEST_DECISIONS_MODELS` to a comma-separated list to select other configured
models. `TEST_DECISIONS_NATIVE_MODEL` overrides the native reference model.
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
| `aspect_ratio`     | String | Aspect ratio (e.g. `1:1`, `16:9`); overrides `size`   |
| `quality`          | String | `low`, `medium`, `high`, `xhigh`, `max` (`standard`/`hd` accepted) |
| `resolution`       | String | `512`, `1K`, `2K`, `4K`                                |
| `background`       | String | `transparent` or `opaque`                             |
| `output_format`    | String | `png`, `jpeg`, or `webp`                              |
| `response_format`  | String | url or b64_json                                       |

Geometry and quality are provider-neutral: `size`/`aspect_ratio`/`quality`/`resolution` are mapped to each model's supported values (aspect ratios snap to the nearest available, unsupported options are dropped), so the same request works across providers.

## Image Edit

**Endpoint:** `POST /v1/images/edits`

| Parameter          | Type   | Description                                            |
|--------------------|--------|-------------------------------------------------------|
| `model`            | String | Model ID                                              |
| `prompt`           | String | Edit instructions                                    |
| `image`            | File   | Image to edit                                         |
| `size`             | String | Pixel size (e.g. `1024x1536`) or `auto`               |
| `aspect_ratio`     | String | Aspect ratio (e.g. `1:1`, `16:9`); overrides `size`   |
| `quality`          | String | `low`, `medium`, `high`, `xhigh`, `max` (`standard`/`hd` accepted) |
| `resolution`       | String | `512`, `1K`, `2K`, `4K`                                |
| `background`       | String | `transparent` or `opaque`                             |
| `output_format`    | String | `png`, `jpeg`, or `webp`                              |
| `response_format`  | String | url or b64_json                                       |

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
| `query`     | String  | Search query           |
| `limit`     | Integer | Maximum results        |
| `category`  | String  | Provider vertical (e.g. `news`, `publication`); see provider categories |
| `location`  | String  | ISO 3166-1 alpha-2 country code to bias results |
| `domain`    | String  | Repeatable; restrict to a domain, or exclude one with a `!` prefix |
| `since`     | String  | Earliest publication date (RFC 3339 or `YYYY-MM-DD`) |
| `until`     | String  | Latest publication date; `YYYY-MM-DD` includes the whole UTC day, RFC 3339 is an exclusive timestamp bound |

```bash
curl -X POST -F "input=your query" -F "category=news" -F "since=2026-10-01" http://localhost:8080/v1/search
```

Search options vary by provider. Unsupported filters return an error rather than
being silently ignored.

| Provider | Categories | Location | Date filters | Result limit |
|----------|------------|----------|--------------|--------------|
| Exa | `company`, `people`, `news`, `publication`, `personal site`, `financial report`; other strings are hints. Legacy `research paper` maps to `publication`. | ISO country code | Supported except with `company` or `people`, which also reject domain exclusions | 1–100 |
| Tavily | `general` (default), `news`, `finance`; legacy `financial report` maps to `finance` | Supported country code, translated to Tavily's country name; `general` only | Strict filtering by estimated publication/update dates; undated results are omitted when bounds are supplied | 0–20 |
| DuckDuckGo | General web search only | Supported country code, translated to a documented region/locale | Not supported by the HTML adapter | Positive integer |
| Custom | Forwarded to the gRPC provider | Forwarded to the gRPC provider | Not present in the current gRPC protocol | Positive int32 |

Exa supports up to 1200 included and 1200 excluded domains. Tavily supports up
to 300 included and 150 excluded domains. DuckDuckGo accepts hostnames, searches
each included domain separately, combines the results, and enforces exclusions.
Publication dates may be unavailable; Tavily dates are estimates of publication
or the last update. Exa and Tavily preserve explicit timestamp bounds, with
Tavily applying timestamp filtering after its calendar-date search.

Provider references: [Exa Search](https://exa.ai/docs/reference/search),
[Tavily Search](https://docs.tavily.com/documentation/api-reference/endpoint/search),
[DuckDuckGo parameters](https://duckduckgo.com/duckduckgo-help-pages/settings/params),
[DuckDuckGo search syntax](https://duckduckgo.com/duckduckgo-help-pages/results/syntax),
and [custom searcher protocol](pkg/searcher/custom/searcher.proto).

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

Evaluate application state with typed questions using a configured native decision,
completion, or embedding model. Requests and responses follow the [TypeSafe System One format](https://docs.typesafe.ai/api).

| Parameter | Type | Description |
|-----------|------|-------------|
| `model` | String | Required model ID or alias |
| `state` | Object, string, array, or null | Required state to evaluate |
| `questions` | Object | Required, nonempty map of question IDs to questions |

Each question has a `type`, optional `instructions`, and `criteria` as required
for its type. Instructions and criterion descriptions may be text, structured
JSON, or null.

| Type | Criteria | Answer |
|------|----------|--------|
| `noul` (yes/no) | Optional object with `true` and/or `false` descriptions | `noul`: probability of yes |
| `choice` | Object of 1–255 named options with descriptions | `choice`: selected option; `probabilities`, `confidence` |
| `score` | Array of 2–10 ordered level descriptions | `score`: weighted level index; `legend`, `probabilities`, `confidence` |

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
