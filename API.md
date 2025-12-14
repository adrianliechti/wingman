# OpenAI Compatible API

OpenAI-compatible endpoints. See [OpenAI API Reference](https://platform.openai.com/docs/api-reference) for full documentation.

## Models

**Endpoints:** `GET /v1/models`, `GET /v1/models/{id}`

List available models.

## Responses

**Endpoint:** `POST /v1/responses`

| Parameter           | Type         | Description                                         |
|---------------------|--------------|-----------------------------------------------------|
| `model`             | String       | Model ID                                            |
| `input`             | String/Array | Input text or conversation messages                 |
| `stream`            | Boolean      | Enable streaming                                    |
| `temperature`       | Float        | Sampling temperature                                |
| `max_output_tokens` | Integer      | Maximum tokens to generate                          |
| `tools`             | Array        | Available tools/functions                           |
| `instructions`      | String       | System instructions                                 |
| `reasoning`         | Object       | Reasoning configuration                             |

## Chat Completions

**Endpoint:** `POST /v1/chat/completions`

| Parameter                | Type         | Description                                         |
|--------------------------|--------------|-----------------------------------------------------|
| `model`                  | String       | Model ID                                            |
| `messages`               | Array        | Conversation messages                               |
| `stream`                 | Boolean      | Enable streaming                                    |
| `temperature`            | Float        | Sampling temperature                                |
| `max_completion_tokens`  | Integer      | Maximum tokens to generate                          |
| `stop`                   | String/Array | Stop sequences                                      |
| `tools`                  | Array        | Available tools/functions                           |
| `response_format`        | Object       | Response format (text, json_object, json_schema)    |
| `reasoning_effort`       | String       | Reasoning effort (low, medium, high)                |

## Embeddings

**Endpoint:** `POST /v1/embeddings`

| Parameter   | Type         | Description       |
|-------------|--------------|-------------------|
| `model`     | String       | Model ID          |
| `input`     | String/Array | Text(s) to embed  |

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

| Parameter   | Type   | Description     |
|-------------|--------|-----------------|
| `model`     | String | Model ID        |
| `file`      | File   | Audio file      |
| `language`  | String | Audio language  |

## Image Generation

**Endpoint:** `POST /v1/images/generations`

| Parameter          | Type   | Description        |
|--------------------|--------|--------------------|
| `model`            | String | Model ID           |
| `prompt`           | String | Image description  |
| `response_format`  | String | url or b64_json    |

## Image Edit

**Endpoint:** `POST /v1/images/edits`

| Parameter   | Type   | Description         |
|-------------|--------|---------------------|
| `model`     | String | Model ID            |
| `prompt`    | String | Edit instructions   |
| `image`     | File   | Image to edit       |

---

# Utility APIs

## Extract

Extract text from files or URLs.

**Endpoint:** `POST /v1/extract`

| Parameter   | Type   | Description                             |
|-------------|--------|-----------------------------------------|
| `file`      | File   | Document to extract text from           |
| `url`       | String | URL to scrape and extract               |
| `schema`    | JSON   | JSON schema for structured extraction   |
| `model`     | String | Model/provider to use                   |

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

| Parameter   | Type     | Description                 |
|-------------|----------|-----------------------------|
| `input`     | String   | Text description to render  |
| `file`      | File(s)  | Optional reference images   |
| `model`     | String   | Model/provider to use       |

```bash
curl -X POST -F "input=a sunset over mountains" http://localhost:8080/v1/render
```

## Search

Search for information.

**Endpoint:** `POST /v1/search` (alias: `/v1/retrieve`)

| Parameter   | Type   | Description            |
|-------------|--------|------------------------|
| `input`     | String | Search query           |
| `model`     | String | Model/provider to use  |

```bash
curl -X POST -F "input=your query" http://localhost:8080/v1/search
```

## Research

Deep research on a topic.

**Endpoint:** `POST /v1/research`

| Parameter   | Type   | Description              |
|-------------|--------|--------------------------|
| `input`     | String | Research topic/question  |
| `model`     | String | Model/provider to use    |

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
| `input`            | String  | Text to segment               |
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
| `input`     | String | Text to summarize               |
| `file`      | File   | File to extract and summarize   |
| `url`       | String | URL to scrape and summarize     |
| `model`     | String | Model/provider to use           |

```bash
curl -X POST -F "input=long article text" http://localhost:8080/v1/summarize
```

## Translate

Translate text or files to another language.

**Endpoint:** `POST /v1/translate`

| Parameter   | Type   | Description            |
|-------------|--------|------------------------|
| `input`     | String | Text to translate      |
| `file`      | File   | File to translate      |
| `language`  | String | Target language        |
| `model`     | String | Model/provider to use  |

```bash
curl -X POST -F "input=Hello world" -F "language=de" http://localhost:8080/v1/translate
```

## Transcribe

Transcribe audio files to text.

**Endpoint:** `POST /v1/transcribe`

| Parameter   | Type   | Description                |
|-------------|--------|----------------------------|
| `file`      | File   | Audio file to transcribe   |
| `language`  | String | Audio language (optional)  |
| `model`     | String | Model/provider to use      |

```bash
curl -X POST -F "file=@audio.mp3" http://localhost:8080/v1/transcribe
```
