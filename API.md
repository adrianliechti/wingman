# OpenAI Compatible API

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
| `temperature`            | Float        | Sampling temperature                                |
| `max_completion_tokens`  | Integer      | Maximum tokens to generate                          |
| `stop`                   | String/Array | Stop sequences                                      |
| `tools`                  | Array        | Available tools/functions                           |
| `tool_choice`            | String/Object| Tool selection mode (auto, none, required)          |
| `parallel_tool_calls`    | Boolean      | Enable parallel tool execution                      |
| `response_format`        | Object       | Response format (text, json_object, json_schema)    |
| `reasoning_effort`       | String       | Reasoning effort (none, minimal, low, medium, high, xhigh, max) |
| `verbosity`              | String       | Output verbosity (low, medium, high)                |

## Responses

**Endpoint:** `POST /v1/responses`

| Parameter              | Type         | Description                                         |
|------------------------|--------------|-----------------------------------------------------|
| `model`                | String       | Model ID                                            |
| `input`                | String/Array | Input text or conversation messages                 |
| `stream`               | Boolean      | Enable streaming                                    |
| `temperature`          | Float        | Sampling temperature                                |
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
| `quality`          | String | `low`, `medium`, `high` (`standard`/`hd` accepted)    |
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
| `quality`          | String | `low`, `medium`, `high` (`standard`/`hd` accepted)    |
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
| `temperature`         | Float        | Sampling temperature                           |
| `top_p`               | Float        | Nucleus sampling                               |
| `top_k`               | Integer      | Top-k sampling                                 |
| `stop_sequences`      | Array        | Stop sequences                                 |
| `tools`               | Array        | Available tools/functions                      |
| `tool_choice`         | Object       | Tool selection (auto, any, tool, none)         |
| `metadata`            | Object       | Request metadata (`user_id`)                   |
| `output_format`       | Object       | Structured output with JSON schema             |
| `thinking`            | Object       | Thinking configuration (type, budget_tokens)   |
| `context_management`  | Object       | Context management with compaction edits       |

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
| `temperature`        | Float   | Sampling temperature                        |
| `topP`               | Float   | Nucleus sampling                            |
| `topK`               | Integer | Top-k sampling                              |
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
| `quality`       | String   | `low`, `medium`, `high`                             |
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

| Parameter   | Type   | Description            |
|-------------|--------|------------------------|
| `model`     | String | Model/provider to use  |
| `query`     | String | Search query           |

```bash
curl -X POST -F "input=your query" http://localhost:8080/v1/search
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
