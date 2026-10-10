
# Wingman

<img src="docs/icon.png" width="150"/>

**A unified LLM platform — one API, many providers, zero lock-in.**

Wingman is an open-source inference hub that simplifies building and deploying large language model (LLM) applications at scale. It fronts every major model vendor and local runtime behind a single OpenAI-, Anthropic- and Gemini-compatible API — with RAG, agents, tools, MCP, routing, rate limiting and OpenTelemetry wired in by configuration alone.

## Key Features

### Multi-Provider Support

The platform integrates with a wide range of LLM providers:

**Chat/Completion Models:**
- OpenAI Platform and Azure OpenAI Service (GPT models)
- Anthropic (Claude models)
- Google Gemini
- AWS Bedrock
- Mistral AI
- xAI (Grok models)
- OpenRouter, NVIDIA NIM and any OpenAI-compatible endpoint
- Local deployments: Ollama, LLAMA.CPP
- Custom models via gRPC plugins

**Embedding Models:**
- OpenAI, Azure OpenAI, Google Gemini, Mistral AI
- Local: Ollama, LLAMA.CPP
- Custom embedders via gRPC

**Media Processing:**
- Image generation: OpenAI DALL-E, Google Gemini, xAI
- Speech-to-text: OpenAI Whisper, Mistral, Azure Speech
- Text-to-speech: OpenAI TTS, Azure Speech, xAI

### Document Processing & RAG

**Document Extractors:**
- Built-in default extractor (PDF, Office documents, email, plain text)
- Azure Document Intelligence
- Docling for document conversion
- Kreuzberg for document parsing
- Mistral document extraction
- LLM-based extraction using any vision/chat model
- Text extraction from plain files
- Custom extractors via gRPC

**Text Segmentation:**
- Kreuzberg segmenter
- Text-based chunking with configurable sizes
- Custom segmenters via gRPC

**Information Retrieval:**
- Web search: DuckDuckGo, Exa, Tavily
- Custom retrievers via gRPC plugins

### Advanced AI Workflows

**Chains & Agents:**
- Agent/Assistant chains with tool calling capabilities
- Custom conversation flows
- Multi-step reasoning workflows
- Tool integration and function calling

**Tools & Function Calling:**
- Built-in tools: search, scraper, research, translator
- **Model Context Protocol (MCP) support**: Full server and client implementation
  - Connect to external MCP servers as tool providers
  - Built-in MCP server exposing platform capabilities
  - Multiple transport methods (HTTP streaming, SSE)
- Custom tools via gRPC plugins

**Additional Capabilities:**
- Typed decisions (Noul, Choice, Score) using existing chat or embedding models
- Text summarization (via chat models)
- Language translation
- Content rendering and formatting

### Infrastructure & Operations

**Routing & Load Balancing:**
- Round-robin load balancer for distributing requests
- Model fallback strategies
- Request routing across multiple providers

**Rate Limiting & Control:**
- Per-provider and per-model rate limiting
- Request throttling and queuing
- Resource usage controls

**Authentication & Security:**
- Static token authentication
- OpenID Connect (OIDC) integration
- Secure credential management

**API Compatibility:**
- OpenAI-compatible API endpoints
- Custom API configurations
- Multiple API versions support

**Observability & Monitoring:**
- Full OpenTelemetry integration
- Request tracing across all components
- Comprehensive metrics and logging
- Performance monitoring and debugging

### Flexible Configuration

Developers can define providers, models, credentials, document processing pipelines, tools, and advanced AI workflows using YAML configuration files. This approach streamlines integration and makes it easy to manage complex AI applications.


## Architecture

![Architecture](docs/architecture.png)

> Source: [`docs/architecture.html`](docs/architecture.html) · Regenerate with `task docs:render`.

The architecture is designed to be modular and extensible, allowing developers to plug in different providers and services as needed. It consists of key components:

**Core Providers:**
- **Completers**: Chat/completion models for text generation and reasoning
- **Embedders**: Vector embedding models for semantic understanding
- **Renderers**: Image generation and visual content creation
- **Synthesizers**: Text-to-speech and audio generation
- **Transcribers**: Speech-to-text and audio processing
- **Rerankers**: Result ranking and relevance scoring

**Document & Data Processing:**
- **Extractors**: Document parsing and content extraction from various formats
- **Segmenters**: Text chunking and semantic segmentation for RAG
- **Retrievers**: Web search and information retrieval
- **Summarizers**: Content compression and summarization
- **Translators**: Multi-language text translation

**AI Workflows & Tools:**
- **Chains**: Multi-step AI workflows and agent-based reasoning
- **Tools**: Function calling, web search, document processing, and custom capabilities
- **APIs**: Multiple API formats and compatibility layers

**Infrastructure:**
- **Routers**: Load balancing and request distribution
- **Rate Limiters**: Resource control and throttling
- **Authorizers**: Authentication and access control
- **Observability**: OpenTelemetry tracing and monitoring

## Use Cases

- **Enterprise AI Applications**: Unified platform for multiple AI services and models
- **RAG (Retrieval-Augmented Generation)**: Document processing, semantic search, and knowledge retrieval
- **AI Agents & Workflows**: Multi-step reasoning, tool integration, and autonomous task execution
- **Scalable LLM Deployment**: High-volume applications with load balancing and failover
- **Multi-Modal AI**: Combining text, image, and audio processing capabilities
- **Custom AI Pipelines**: Flexible workflows using custom tools and chains


## Quick Start

Everything is driven by a single `config.yaml`. Define providers, then layer on tools, agents and pipelines as needed.

```yaml
# config.yaml — a complete, working example

providers:
  # A hosted vendor — list the models you want to expose
  - type: openai
    token: ${OPENAI_API_KEY}
    models:
      - gpt-6-astra
      - gpt-6.1-sol
      - gpt-6-sol
      - gpt-6-luna
      - gpt-5.4
      - gpt-5.4-mini
      - text-embedding-3-large

  # Another vendor, aliased to friendly names
  - type: anthropic
    token: ${ANTHROPIC_API_KEY}
    models:
      - claude-opus-5-5
      - claude-sonnet-5-5
      - claude-sonnet-4-6
      - claude-haiku-4-5

  # A local runtime via the OpenAI-compatible API
  - type: ollama
    url: http://localhost:11434
    models:
      local-devstral:
        id: devstral-small-2:24b

# Web access for RAG / agents
searchers:
  web:
    type: exa
    token: ${EXA_API_KEY}

scrapers:
  web:
    type: exa
    token: ${EXA_API_KEY}

# Wrap them as callable tools
tools:
  web_search:
    type: search
    searcher: web
  web_fetch:
    type: scraper
    scraper: web

# A ready-to-call assistant with tools and a system prompt
agents:
  wingman:
    type: assistant
    model: claude-sonnet-4-6
    effort: medium
    tools:
      - web_search
      - web_fetch
    messages:
      - role: system
        content: |
          You are Wingman, a helpful assistant.
          Current date: {{ now | date "2006-01-02" }}
```

Run the server (reads `.env` for the referenced secrets):

```shell
task server        # or: go run .
```

Call it with any OpenAI-compatible client — agents appear as regular models:

```shell
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{ "model": "wingman", "messages": [{ "role": "user", "content": "What changed in the news today?" }] }'
```

### API Surface

A single ingress speaks four dialects, so existing SDKs work unchanged:

| Family | Mount | Endpoints |
| --- | --- | --- |
| **OpenAI** (compatible) | `/v1` | `chat/completions`, `responses`, `decisions`, `embeddings`, `audio/{speech,transcriptions}`, `images/{generations,edits}`, `models` |
| **Anthropic** (compatible) | `/v1` | `messages`, `messages/count_tokens` |
| **Gemini** (compatible) | `/v1beta` | `models/{model}:generateContent`, `:streamGenerateContent`, `:countTokens` |
| **MCP** (native) | `/v1` | `mcp/{name}` — each configured MCP server, over HTTP-stream or SSE |
| **Wingman** (native) | `/v1` | `systemone`, `extract`, `segment`, `search`, `retrieve`, `research`, `rerank`, `summarize`, `translate`, `render`, `transcribe` |

`POST /v1/systemone` accepts TypeSafe state and typed questions using a native
`typesafe` provider or an adapted completion or embedding model. TypeSafe SDKs
can point their base URL at Wingman and use a configured Wingman model.
See [System One](API.md#system-one) for the request format and response fields.

`POST /v1/decisions` accepts OpenAI's ordered predicate, choice, and score
questions. Configure an OpenAI model with `type: decider` to call the native
Decisions API; see [Decisions](API.md#decisions).


## Integrations & Configuration

### LLM Providers

Wingman uses each model's default sampling behavior. Temperature, top-p, and
top-k are not configurable through provider options, agent configuration, or API
requests. Use reasoning effort to control reasoning where the model supports it.

#### OpenAI Platform

https://platform.openai.com/docs/api-reference

```yaml
providers:
  - type: openai
    token: sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

    models:
      - gpt-6-astra
      - gpt-6.1-sol
      - gpt-6-sol
      - gpt-6-luna
      - gpt-4o
      - gpt-4o-mini
      - text-embedding-3-small
      - text-embedding-3-large
      - whisper-1
      - dall-e-3
      - tts-1
      - tts-1-hd
```

GPT-6.1 Sol uses `gpt-6.1-sol`. The `openai` provider uses the Responses API upstream, including for tool calls received through Wingman's Chat Completions and Anthropic endpoints. Reasoning defaults to `medium`; `low`, `high`, `xhigh`, and `max` are also supported. Wingman maps `none` and `minimal` to `low`. OpenAI's Chat Completions endpoint supports this model only without tools. See the [official model documentation](https://developers.openai.com/api/docs/models/gpt-6.1-sol).


#### Azure OpenAI Service

https://azure.microsoft.com/en-us/products/ai-services/openai-service

```yaml
providers:
  - type: openai
    url: https://xxxxxxxx.openai.azure.com
    token: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

    models:
      # https://docs.anthropic.com/en/docs/models-overview
      #
      # {alias}:
      #   - id: {azure oai deployment name}

      gpt-3.5-turbo:
        id: gpt-35-turbo-16k

      gpt-4:
        id: gpt-4-32k
        
      text-embedding-ada-002:
        id: text-embedding-ada-002
```


#### Anthropic

https://www.anthropic.com/api

```yaml
providers:
  - type: anthropic
    token: sk-ant-apixx-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

    # https://platform.claude.com/docs/en/models/overview
    #
    # {alias}:
    #   - id: {anthropic api model name}
    models:
      - claude-haiku-5-5
      - claude-sonnet-5-5
      - claude-opus-5-5
```


#### Google Gemini

Gemini completions use the [Interactions API](https://ai.google.dev/gemini-api/docs/interactions-overview)
with full conversation history and `store=false`. Tool calls, results, and thought
signatures are replayed as execution steps. Reasoning effort maps to thinking
levels: `minimal`, `low`, and disabled thinking use `low`, `medium` stays `medium`, and
`high`, `xhigh`, and `max` use `high`. Omitted effort keeps the model default.
Sampling parameters are omitted, following the current
[Gemini migration guidance](https://ai.google.dev/gemini-api/docs/latest-model#migration-checklist).
Embeddings, image rendering, transcription, speech synthesis, and Live
realtime retain their dedicated integrations.

To compare Interactions against `generateContent` with real API calls, run
`WINGMAN_GOOGLE_LIVE=1 go test -v ./pkg/provider/google -run TestComplete_LiveComparison -count=1`.
The suite uses `GEMINI_API_KEY` from the environment or `.env` and defaults to
`gemini-3.8-flash`; `WINGMAN_GOOGLE_LIVE_MODELS` accepts a comma-separated list.
Live checks on Gemini 2.5 Flash found that Interactions rejects `medium` and
does not enforce the current structured-output format on that model.

```yaml
providers:
  - type: gemini
    token: ${GOOGLE_API_KEY}

    # https://ai.google.dev/gemini-api/docs/models/gemini
    #
    # {alias}:
    #   - id: {gemini api model name}
    models:
      - gemini-3.8-flash
      - gemini-3.8-flash-tts
      - gemini-3.5-transcribe
      - gemini-3.1-flash-image
      - gemini-embedding-2
```


#### AWS Bedrock

Claude models use the Converse API, which supports inline PDF, Word (`doc`/`docx`),
Excel (`xls`/`xlsx`), CSV, HTML, text, and Markdown documents. Include a related
text prompt with the document. See the [AWS document-input reference](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_DocumentBlock.html).

The automatic message cache checkpoint precedes trailing non-PDF documents:
Bedrock's Claude translation rejects a checkpoint immediately after those
documents. Document bytes and formats are preserved; a trailing text block or
PDF still allows caching the entire message. See the [upstream Bedrock report](https://github.com/strands-agents/harness-sdk/issues/1966).

```yaml
providers:
  - type: bedrock
    # AWS credentials configured via environment or IAM roles

    models:
      claude-3-sonnet:
        id: anthropic.claude-3-sonnet-20240229-v1:0
```


#### Mistral AI

```yaml
providers:
  - type: mistral
    token: ${MISTRAL_API_KEY}

    # https://docs.mistral.ai/getting-started/models/
    #
    # {alias}:
    #   - id: {mistral api model name}
    models:
      mistral-large:
        id: mistral-large-latest
```


#### Azure Speech

https://learn.microsoft.com/en-us/azure/ai-services/speech-service/

Text-to-speech and speech-to-text using Azure Cognitive Services Speech. Supports multilingual voices with automatic language detection. OpenAI voice names (alloy, echo, fable, nova, onyx, shimmer) are automatically mapped to Azure equivalents.

```yaml
providers:
  - type: azurespeech
    token: ${AZURE_SPEECH_KEY}
    vars:
      region: eastus
    models:
      azure-tts:
        id: azure-tts
        type: synthesizer
      azure-stt:
        id: azure-stt
        type: transcriber
```

The `region` variable is used to construct the appropriate endpoints:
- TTS: `https://{region}.tts.speech.microsoft.com`
- STT: `https://{region}.api.cognitive.microsoft.com`


#### Ollama

https://ollama.ai

```shell
$ ollama start
$ ollama run mistral
```

```yaml
providers:
  - type: ollama
    url: http://localhost:11434

    # https://ollama.com/library
    #
    # {alias}:
    #   - id: {ollama model name with optional version}
    models:
      mistral-7b-instruct:
        id: mistral:latest
```


#### LLAMA.CPP

https://github.com/ggerganov/llama.cpp/tree/master/examples/server

```shell
$ llama-server --port 9081 --log-disable --model ./models/mistral-7b-instruct-v0.2.Q4_K_M.gguf
```

```yaml
providers:
  - type: llama
    url: http://localhost:9081

    models:
      - mistral-7b-instruct
```


#### xAI

https://x.ai/api

```yaml
providers:
  - type: xai
    token: ${XAI_API_KEY}

    models:
      - grok-4.20-reasoning
      - grok-imagine-image  # renderer
      - grok-tts            # synthesizer
```


#### OpenRouter & OpenAI-compatible Endpoints

Any OpenAI-compatible endpoint (OpenRouter, vLLM, LM Studio, NVIDIA NIM, a self-hosted gateway, …) works by pointing `url` at it. Use the `openai` provider for a drop-in endpoint, or `openrouter` / `nim` where a dedicated adapter exists.

```yaml
providers:
  - type: openai
    url: https://openrouter.ai/api/v1
    token: ${OPENROUTER_API_KEY}

    models:
      glm-air:
        id: z-ai/glm-4.6-air
```


#### TypeSafe / System One

The `typesafe` provider calls a native decision API and preserves its returned
probabilities, confidence, model, and token usage. `url` is the complete evaluation
endpoint; it defaults to `https://api.typesafe.ai/v1/systemone`.
Models under this provider default to the `decider` role.

```yaml
providers:
  - type: typesafe
    url: http://localhost:11434/v1/systemone
    models:
      - nimble

  - type: typesafe
    url: https://openrouter.ai/api/alpha/decisions
    token: ${OPENROUTER_API_KEY}
    models:
      jev-1.13:
        id: typesafe/jev-1.13
```

The local endpoint must implement the System One API. To use TypeSafe's hosted
API, omit `url`, set `token: ${TYPESAFE_API_KEY}`, and configure `jev-latest`.
Native decision models are listed in `/v1/models` and used through
`/v1/systemone`. They support `max_retries` at provider or model level.

> **Provider interfaces.** Each model serves one of seven roles, inferred from its `type` or set explicitly per model: **completer** (chat/reason), **decider** (typed decisions), **embedder** (vectors), **renderer** (text→image), **synthesizer** (text→speech), **transcriber** (speech→text), **reranker** (relevance). See [`docs/architecture.png`](docs/architecture.png) for the interface × backend matrix.


### Routers

A router exposes several models under one id and distributes requests across them — useful for load balancing and failover across providers. Types: `roundrobin` (even rotation) and `adaptive` (prefers healthy/faster backends).

Routers protect backends with a circuit breaker and fail over transparently: if a provider errors or produces no output within `first_token_timeout` (default `2m`), the request is retried on the next healthy provider before any error reaches the client.

```yaml
routers:
  fast-lb:
    type: roundrobin       # or: adaptive
    models:
      - gpt-5.4-mini
      - claude-haiku-4-5
      - local-devstral
    # fallback: some-model         # used when all providers are unavailable
    # first_token_timeout: 30s     # fail over if no output arrives in time
    # failure_threshold: 5         # consecutive failures before a circuit opens
    # recovery_timeout: 30s        # wait before probing an open circuit
```

> [!TIP]
> Set `max_retries: 0` on models used as router members. Provider SDKs retry rate limits in place (honoring `Retry-After`, which can mean waiting 30s+ on the same backend) — disabling SDK retries lets the router fail over to another backend immediately.


### Web Access (Search · Scrape · Research)

Web access comes in three flavours. A **searcher** returns result lists, a **scraper** fetches and cleans a single URL, and a **researcher** runs a full multi-step research loop. Each is referenced by name from `tools` (see [Tools & Function Calling](#tools--function-calling)).

#### Searchers

Return ranked search results. Types: `duckduckgo`, `exa`, `tavily`, `custom`.

```yaml
searchers:
  web:
    type: exa            # or: duckduckgo · tavily · custom
    token: ${EXA_API_KEY}
```

#### Scrapers

Fetch and extract clean content from a URL. Types: `fetch` (built-in HTTP), `exa`, `tavily`, `custom`.

```yaml
scrapers:
  web:
    type: fetch          # or: exa · tavily · custom

  reader:
    type: tavily
    token: ${TAVILY_API_KEY}
```

#### Researchers

Run an end-to-end research workflow. Types: `exa`, `openai`, `anthropic`, `perplexity`, `custom`, or the built-in `agent` that orchestrates your own model with a searcher + scraper.

```yaml
researchers:
  # Hosted deep-research endpoints
  web:
    type: exa
    token: ${EXA_API_KEY}

  # Build your own from any completer + web access
  agent:
    type: agent
    model: gpt-5.4-mini
    searcher: web
    scraper: web
    effort: medium
```


### Document Extraction

Extraction providers implement `Capabilities()` with media types, extensions, and `UnknownFormats`. `MaySupport(file)` selects candidates; the provider validates the actual contents. `UnknownFormats` keeps unlisted files eligible for provider validation, whether the provider inspects contents or delegates to a service whose formats are unknown. The multi-extractor skips incompatible providers, tries candidates in order, and preserves failures when none succeed. Cancellation stops fallback.

#### Default Extractor

Built-in extraction without external services. Uses `go-extract` for PDF,
OOXML, HTML, EML and MSG documents, with a plain-text fallback. Email
attachments are extracted recursively. Used automatically when no extractors
are configured.

```yaml
extractors:
  default:
    type: default
```


#### Azure Document Intelligence

```yaml
extractors:
  azure:
    type: azure
    url: https://YOUR_INSTANCE.cognitiveservices.azure.com
    token: ${AZURE_API_KEY}
```


#### Docling Extractor

[Docling Serve](https://github.com/docling-project/docling-serve). When API-key authentication is enabled, `token` supplies `X-Api-Key` for conversion, polling, and results.

```yaml
extractors:
  docling:
    type: docling
    url: http://localhost:5000
```


#### Kreuzberg Extractor

https://github.com/lenskit/kreuzberg

```yaml
extractors:
  kreuzberg:
    type: kreuzberg
    url: http://localhost:8000
```


#### Mistral Extractor

Supports PDF, DOCX, PPTX, PNG, JPEG, and AVIF using the fixed Mistral API endpoint. `model` selects the OCR model and defaults to `mistral-ocr-latest`.

```yaml
extractors:
  mistral:
    type: mistral
    token: ${MISTRAL_API_KEY}
```


#### LLM Extractor

Use any configured vision/chat model to extract document content.

```yaml
extractors:
  llm:
    type: llm
    model: gpt-5.4-mini
```


#### Text Extractor

```yaml
extractors:
  text:
    type: text
```


#### Custom Extractor

```yaml
extractors:
  custom:
    type: custom
    url: grpc://localhost:9000
```

Custom extractors report media types, extensions, and `unknown_formats` through the gRPC `Capabilities` RPC. Clients load this record once during construction. Servers that do not implement discovery remain eligible for all formats; other discovery failures prevent client initialization. Capability declarations belong to the service, with no YAML or Go option override.

### Text Segmentation

#### Kreuzberg Segmenter

```yaml
segmenters:
  kreuzberg:
    type: kreuzberg
    url: http://localhost:8000
```


#### Text Segmenter

```yaml
segmenters:
  text:
    type: text
    chunkSize: 1000
    chunkOverlap: 200
```


#### Custom Segmenter

```yaml
segmenters:
  custom:
    type: custom
    url: http://localhost:8080
```


### AI Agents

Agents wrap a completer with a system prompt, tools and a control loop, and are then exposed as a regular model id (use the agent's key as the `model` in any request). Two loop types are available:

- **`assistant`** — a tool-calling loop that runs tools until the model produces a final answer.
- **`react`** — an explicit reason → act → observe loop.

```yaml
agents:
  assistant:
    type: assistant
    model: gpt-5.4          # any configured completer (or router / another agent)

    effort: medium          # reasoning effort: minimal · low · medium · high
    verbosity: medium       # output verbosity: low · medium · high

    tools:
      - web_search
      - web_fetch

    messages:
      - role: system
        content: |
          You are a helpful AI assistant.
          Current date: {{ now | date "2006-01-02" }}

  researcher:
    type: react
    model: claude-sonnet-4-6
    tools:
      - web_research
```

System prompts are Go templates — helpers like `{{ now | date "2006-01-02" }}` are evaluated per request.


### Tools & Function Calling

#### Model Context Protocol (MCP)

The platform provides comprehensive support for the Model Context Protocol (MCP), enabling integration with MCP-compatible tools and services.

**MCP Server Support:**
- Built-in MCP server that exposes platform tools to MCP clients
- Automatic tool discovery and schema generation
- Multiple transport methods (HTTP streaming, SSE, command-line)

**MCP Client Support:**
- Connect to external MCP servers as tool providers
- Support for various MCP transport methods
- Automatic tool registration and execution

**Consume an external MCP server as tools** — point a `mcp` tool at any HTTP-streaming or SSE MCP endpoint; its tools are discovered and registered automatically:

```yaml
tools:
  # HTTP streaming (/mcp) or SSE (/sse) — transport is auto-detected
  github:
    type: mcp
    url: https://api.example.com/mcp
    vars:
      api-key: ${API_KEY}   # forwarded as a header to the server
```

**Expose your own tools as an MCP server** — group tools under `mcps`; each is served at `/v1/mcp/{name}` for any MCP client (IDEs, agents) to consume:

```yaml
mcps:
  web:
    type: server          # built-in server exposing the listed tools
    name: web
    tools:
      - web_search
      - web_fetch
      - web_research

  # Or reverse-proxy an upstream MCP server
  upstream:
    type: proxy
    url: https://api.example.com/mcp
```

#### Built-in Tools

Built-in tools wrap the providers you configured elsewhere. Valid types: `search`, `scraper` (alias `crawler`), `research`, `translator`, `mcp`, `custom`.

The Go search, extraction, and translation provider interfaces require their family's typed `Capabilities()` method. Search categories are part of that record. Tool schemas, routing, caching, and tracing use these records directly; custom Go implementations must supply them. Request-specific combinations, file formats, and credentials still require provider validation. Optional interfaces such as `tool.Resulter` remain separate when they add behavior.

Custom searchers report category names, descriptions, and date-filter support through the gRPC `Capabilities` RPC. Search requests carry optional `since` and `until` publication timestamps (inclusive start, exclusive end). Legacy services without discovery expose no category enum or date filters. Discovery runs once at construction with a five-second deadline; errors other than `Unimplemented` fail initialization. The extractor and translator clients follow the same discovery behavior.

| Tool | Model-facing contract |
| --- | --- |
| `web_search` | Pass `query` or up to eight independent `queries` sharing the same filters. `max_results` applies per query (1–10). Date fields are offered only when supported; a date-only `until` includes the entire UTC day. Listed provider categories use an enum; put other topic preferences in the query. Distinct excerpts from the same source are retained. |
| `web_fetch` | Pass an absolute HTTP(S) `url`. `query` selects source passages; a positive `start_index` reads sequentially instead. Character offsets count Unicode characters, and omission notices explain how to continue. |
| `web_research` | Delegate a self-contained question through `instructions`. Returns the configured researcher's report; use available search/fetch tools for targeted lookups. |
| `translate` | Pass original `text` and a target language code in `lang`. Language tags are validated before calling the provider; the result is translation text. |
| MCP | Discovers tools across all pages and preserves text, supported binary resources, and structured results. Authentication is applied to an outgoing request copy. |
| Custom gRPC | Discovers named tools with object parameter schemas. Sends arguments as JSON and decodes JSON responses, including strings and scalars; also accepts YAML objects and plain text. |

```yaml
tools:
  web_search:
    type: search
    searcher: web         # references a searchers: entry

  web_fetch:
    type: scraper
    scraper: web          # references a scrapers: entry

  web_research:
    type: research
    researcher: agent     # references a researchers: entry

  to_english:
    type: translator
    translator: deepl     # references a translators: entry
```


#### Custom Tools

```yaml
tools:
  custom-tool:
    type: custom
    url: grpc://localhost:8080
```


### Authentication

Authorizers run as middleware on every request. With none configured, access is open. Types: `anonymous`, `header`, `static`, `oidc`.

#### Static Tokens

```yaml
authorizers:
  - type: static
    tokens:
      - "your-secret-token"
```

#### Header

Trust an upstream proxy that injects an identity header.

```yaml
authorizers:
  - type: header
```

#### OIDC

```yaml
authorizers:
  - type: oidc
    url: https://your-oidc-provider.com
    audience: your-audience
```


### Rate Limiting

Add rate limiting to any provider, with optional per-model overrides:

```yaml
providers:
  - type: openai
    token: ${OPENAI_API_KEY}
    limit: 10  # requests per second

    models:
      gpt-5.4:
        limit: 5  # override for specific model
```


### Summarization & Translation

#### Automatic Summarization

Summarization is automatically available for any chat model:

```yaml
# Use any completer model for summarization
# The platform automatically adapts chat models for summarization tasks
```


#### Translation

Translators back the `/v1/translate` endpoint and the `translator` tool. Types: `deepl`, `azure`, `google`, `llm` (use any completer), `custom`.

Translation providers implement `Capabilities()` with three modes: `TextToText`, `FileToText`, and `FileToDocument`. Each reports `supported`, `unsupported`, or `unknown`; the zero value is unsupported. DeepL, Azure, and Google translate text and return translated documents for file input. The LLM adapter reads files supported by its model and returns plain text. Capabilities describe modes; providers still validate credentials, formats, and model restrictions when translating.

The `translate` tool requires possible text-to-text support. The API returns text by default or with `Accept: text/plain`. It sends uploads directly to providers with confirmed file-to-text support, including the LLM adapter, and uses the configured extractor before text translation for other text providers. Request a translated document with a document MIME type such as `Accept: application/pdf`; the API checks file-to-document support before calling the provider. Providers that accept files but return only text respond with HTTP 406 for document output requests.

Custom gRPC translators report all three modes through the `Capabilities` RPC using `SUPPORT_SUPPORTED`, `SUPPORT_UNSUPPORTED`, or `SUPPORT_UNKNOWN`. Omitted wire values and legacy services without discovery mean unknown support. Unknown modes allow an attempt without guaranteeing support. With unknown file-to-text support, the API prefers extraction plus text translation when text input is possible. Clients load modes during construction; services own these declarations, with no YAML or Go option override.

```yaml
translators:
  # Dedicated translation API
  deepl:
    type: deepl
    token: ${DEEPL_API_KEY}

  google:
    type: google
    token: ${GOOGLE_API_KEY}

  # Or translate with any configured chat model
  llm:
    type: llm
    model: gpt-5.4-mini
```

Google uses the official [Cloud Translation v3 Go SDK](https://pkg.go.dev/cloud.google.com/go/translate/apiv3) over REST for OAuth text and [document translation](https://docs.cloud.google.com/translate/docs/advanced/translate-documents). API-key text translation uses [Cloud Translation Basic (v2)](https://docs.cloud.google.com/translate/docs/reference/rest/v2/translate). Both detect the source language automatically and default to English when no target language is supplied. `token` accepts either an API key for text translation or a path to a service account JSON file for both text and files. With no token, the provider uses Application Default Credentials (ADC). Files always use OAuth.

Document translation supports PDF, DOC/DOCX, PPT/PPTX, and XLS/XLSX, preserving the file name and returning the translated file in the original format. MIME types can be supplied explicitly or inferred from the file name. Documents can be up to 20 MB. PDF translation uses Google's mode that supports scanned documents, with automatic rotation correction and a 20-page limit. Scanned PDFs and complex layouts may lose formatting. In PDFs that mix native and scanned content, Google leaves the scanned content untranslated.

[API keys only authenticate v2](https://docs.cloud.google.com/translate/docs/authentication). Enable Cloud Translation in your Google Cloud project with billing enabled, and grant your service account the [Cloud Translation API User role](https://docs.cloud.google.com/translate/docs/access-control) (`roles/cloudtranslate.user`). The identity also needs `serviceusage.services.use` on the billing/quota project, for example through `roles/serviceusage.serviceUsageConsumer`.

For a service account deployment, mount its JSON key file into the running container and configure its path as the token:

```yaml
translators:
  google:
    type: google
    token: /run/secrets/google-service-account.json
```

This is enough for both text and files: the project is detected from the JSON file and the location defaults to `global`. Optional `vars.project` and `vars.location` override those values when needed. No PDF mode configuration is required.

Paths are resolved from the server's working directory. Missing paths, invalid JSON, and credential files of another type fail during configuration loading. The path can also come from an environment variable, e.g. `token: ${GOOGLE_TRANSLATE_CREDENTIALS}`. For `.env` loading, run `task server`.

Alternatively, omit `token` and set these environment variables for ADC:

```dotenv
GOOGLE_APPLICATION_CREDENTIALS=/run/secrets/google-service-account.json
GOOGLE_CLOUD_PROJECT=your-project-id
```

Google's authentication library signs the service account's OAuth assertion, obtains access tokens, caches them, and refreshes them automatically. The project is detected from the credential file or ADC; `vars.project` can override it. API-key text translation works without ADC. On Google Cloud, you can attach the service account to the workload and omit `GOOGLE_APPLICATION_CREDENTIALS` to use its managed identity. For local browser login, ADC also supports `gcloud auth application-default login`.

```sh
curl -X POST -F "model=google" -F "input=Hello world" -F "language=de" http://localhost:4242/v1/translate
curl -X POST -H "Accept: application/pdf" -F "model=google" -F "file=@document.pdf" -F "language=de" http://localhost:4242/v1/translate -o translated.pdf
```
