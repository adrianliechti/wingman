
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
- Built-in default extractor (PDF, Office documents, plain text)
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
      - gpt-5.4
      - gpt-5.4-mini
      - text-embedding-3-large

  # Another vendor, aliased to friendly names
  - type: anthropic
    token: ${ANTHROPIC_API_KEY}
    models:
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
task server        # or: go run cmd/server/main.go
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
| **OpenAI** (compatible) | `/v1` | `chat/completions`, `responses`, `embeddings`, `audio/{speech,transcriptions}`, `images/{generations,edits}`, `models` |
| **Anthropic** (compatible) | `/v1` | `messages`, `messages/count_tokens` |
| **Gemini** (compatible) | `/v1beta` | `models/{model}:generateContent`, `:streamGenerateContent`, `:countTokens` |
| **MCP** (native) | `/v1` | `mcp/{name}` — each configured MCP server, over HTTP-stream or SSE |
| **Wingman** (native) | `/v1` | `extract`, `segment`, `search`, `retrieve`, `research`, `rerank`, `summarize`, `translate`, `render`, `transcribe` |


## Integrations & Configuration

### LLM Providers

#### OpenAI Platform

https://platform.openai.com/docs/api-reference

```yaml
providers:
  - type: openai
    token: sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

    models:
      - gpt-4o
      - gpt-4o-mini
      - text-embedding-3-small
      - text-embedding-3-large
      - whisper-1
      - dall-e-3
      - tts-1
      - tts-1-hd
```


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

    # https://docs.anthropic.com/en/docs/models-overview
    #
    # {alias}:
    #   - id: {anthropic api model name}
    models:
      claude-3.5-sonnet:
        id: claude-3-5-sonnet-20240620
```


#### Google Gemini

```yaml
providers:
  - type: gemini
    token: ${GOOGLE_API_KEY}

    # https://ai.google.dev/gemini-api/docs/models/gemini
    #
    # {alias}:
    #   - id: {gemini api model name}
    models:
      - gemini-3.5-flash
      - gemini-3.1-pro-preview
      - gemini-3.1-flash-lite
      - gemini-3.1-flash-image
      - gemini-3-pro-image
      - gemini-embedding-2
```


#### AWS Bedrock

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


> **Provider interfaces.** Each model serves one of six roles, inferred from its `type` or set explicitly per model: **completer** (chat/reason), **embedder** (vectors), **renderer** (text→image), **synthesizer** (text→speech), **transcriber** (speech→text), **reranker** (relevance). See [`docs/architecture.png`](docs/architecture.png) for the full interface × backend matrix.


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

#### Default Extractor

Built-in extraction without external services. Combines the PDF, OOXML and text
extractors and picks the first one matching the input. Used automatically when
no extractors are configured.

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

https://github.com/DS4SD/docling

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


#### PDF Extractor

```yaml
extractors:
  pdf:
    type: pdf
```


#### OOXML Extractor

Word (.docx), Excel (.xlsx) and PowerPoint (.pptx) to Markdown.

```yaml
extractors:
  ooxml:
    type: ooxml
```


#### Custom Extractor

```yaml
extractors:
  custom:
    type: custom
    url: http://localhost:8080
```


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
    # temperature: 0.7

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
    url: http://localhost:8080
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

Translators back the `/v1/translate` endpoint and the `translator` tool. Types: `deepl`, `azure`, `llm` (use any completer), `custom`.

```yaml
translators:
  # Dedicated translation API
  deepl:
    type: deepl
    token: ${DEEPL_API_KEY}

  # Or translate with any configured chat model
  llm:
    type: llm
    model: gpt-5.4-mini
```