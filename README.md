
# LLM Platform

<img src="docs/icon.png" width="150"/>

The LLM Platform or Inference Hub is an open-source product designed to simplify the development and deployment of large language model (LLM) applications at scale. It provides a unified framework that allows developers to integrate and manage multiple LLM vendors, models, and related services through a standardized but highly flexible approach.

## Key Features

### Multi-Provider Support

The platform integrates with a wide range of LLM providers:

**Chat/Completion Models:**
- OpenAI Platform and Azure OpenAI Service (GPT models)
- Anthropic (Claude models)
- Google Gemini
- AWS Bedrock
- Groq
- Mistral AI
- xAI
- Hugging Face
- Local deployments: Ollama, LLAMA.CPP, Mistral.RS
- Custom models via gRPC plugins

**Embedding Models:**
- OpenAI, Azure OpenAI, Jina, Hugging Face, Google Gemini
- Local: Ollama, LLAMA.CPP
- Custom embedders via gRPC

**Media Processing:**
- Image generation: OpenAI DALL-E, Replicate
- Speech-to-text: OpenAI Whisper, Groq Whisper, WHISPER.CPP
- Text-to-speech: OpenAI TTS
- Reranking: Jina

### Document Processing & RAG

**Document Extractors:**
- Apache Tika for various document formats
- Unstructured.io for advanced document parsing
- Azure Document Intelligence
- Jina Reader for web content
- Exa and Tavily for web search and extraction
- Text extraction from plain files
- Custom extractors via gRPC

**Text Segmentation:**
- Jina segmenter for semantic chunking
- Text-based chunking with configurable sizes
- Unstructured.io segmentation
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
- Built-in tools: search, extract, retrieve, render, synthesize, translate
- **Model Context Protocol (MCP) support**: Full server and client implementation
  - Connect to external MCP servers as tool providers
  - Built-in MCP server exposing platform capabilities
  - Multiple transport methods (HTTP streaming, SSE, command execution)
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
      gemini-1.5-pro:
        id: gemini-1.5-pro-latest
      
      gemini-1.5-flash:
        id: gemini-1.5-flash-latest
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


#### Groq

```yaml
providers:
  - type: groq
    token: ${GROQ_API_KEY}

    # https://console.groq.com/docs/models
    #
    # {alias}:
    #   - id: {groq api model name}
    models:
      groq-llama-3-8b:
        id: llama3-8b-8192

      groq-whisper-1:
        id: whisper-large-v3
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


#### xAI

```yaml
providers:
  - type: xai
    token: ${XAI_API_KEY}

    models:
      grok-beta:
        id: grok-beta
```


#### Replicate

https://replicate.com/

```yaml
providers:
  - type: replicate
    token: ${REPLICATE_API_KEY}
    #
    # {alias}:
    #   - id: {cohere api model name}
    models:
      replicate-flux-pro:
        id: black-forest-labs/flux-pro
```


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


#### Mistral.RS

https://github.com/EricLBuehler/mistral.rs

```shell
$ mistralrs-server --port 1234 --isq Q4K plain -m meta-llama/Meta-Llama-3.1-8B-Instruct -a llama
```

```yaml
providers:
  - type: mistralrs
    url: http://localhost:1234

    models:
      mistralrs-llama-3.1-8b:
        id: llama
        
```


#### WHISPER.CPP

https://github.com/ggerganov/whisper.cpp/tree/master/examples/server

```shell
$ whisper-server --port 9083 --convert --model ./models/whisper-large-v3-turbo.bin
```

```yaml
providers:
  - type: whisper
    url: http://localhost:9083

    models:
      - whisper
```


#### Hugging Face

https://huggingface.co/

```yaml
providers:
  - type: huggingface
    token: hf_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
    
    models:
      mistral-7B-instruct:
        id: mistralai/Mistral-7B-Instruct-v0.1
      
      huggingface-minilm-l6-2:
        id: sentence-transformers/all-MiniLM-L6-v2
```


### Routers

#### Round-robin Load Balancer

```yaml
routers:
  llama-lb:
    type: roundrobin
    models:
      - llama-3-8b
      - groq-llama-3-8b
      - huggingface-llama-3-8b
```


### Information Retrieval / Web Search

#### DuckDuckGo

```yaml
retrievers:
  web:
    type: duckduckgo
```


#### Exa

https://exa.ai

```yaml
retrievers:
  exa:
    type: exa
    token: ${EXA_API_KEY}
```


#### Tavily

https://tavily.com

```yaml
retrievers:
  tavily:
    type: tavily
    token: ${TAVILY_API_KEY}
```


#### Custom Retriever

```yaml
retrievers:
  custom:
    type: custom
    url: http://localhost:8080
```


### Document Extraction

#### Tika

```shell
# using Docker
docker run -it --rm -p 9998:9998 apache/tika:3.0.0.0-BETA2-full
```

```yaml
extractors:  
  tika:
    type: tika
    url: http://localhost:9998
    chunkSize: 4000
    chunkOverlap: 200
```


#### Unstructured

https://unstructured.io

```shell
docker run -it --rm -p 9085:8000 quay.io/unstructured-io/unstructured-api:0.0.80 --port 8000 --host 0.0.0.0
```

```yaml
extractors:
  unstructured:
    type: unstructured
    url: http://localhost:9085/general/v0/general
```


#### Azure Document Intelligence

```yaml
extractors:
  azure:
    type: azure
    url: https://YOUR_INSTANCE.cognitiveservices.azure.com
    token: ${AZURE_API_KEY}
```


#### Jina Reader

```yaml
extractors:
  jina:
    type: jina
    token: ${JINA_API_KEY}
```


#### Exa / Tavily Web Extraction

```yaml
extractors:
  exa:
    type: exa
    token: ${EXA_API_KEY}

  tavily:
    type: tavily
    token: ${TAVILY_API_KEY}
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
    url: http://localhost:8080
```


### Text Segmentation

#### Jina Segmenter

```yaml
segmenters:
  jina:
    type: jina
    token: ${JINA_API_KEY}
```


#### Text Segmenter

```yaml
segmenters:
  text:
    type: text
    chunkSize: 1000
    chunkOverlap: 200
```


#### Unstructured Segmenter

```yaml
segmenters:
  unstructured:
    type: unstructured
    url: http://localhost:9085/general/v0/general
```


#### Custom Segmenter

```yaml
segmenters:
  custom:
    type: custom
    url: http://localhost:8080
```


### AI Agents & Chains

#### Agent/Assistant Chain

```yaml
chains:
  assistant:
    type: agent
    model: gpt-4o
    tools:
      - search
      - extract
    messages:
      - role: system
        content: "You are a helpful AI assistant."
```


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

**MCP Tool Configuration:**

```yaml
tools:
  # MCP server via HTTP streaming
  mcp-streamable:
    type: mcp
    url: http://localhost:8080/mcp

  # MCP server via Server-Sent Events
  mcp-sse:
    type: mcp
    url: http://localhost:8080/sse
    vars:
      api-key: ${API_KEY}

  # MCP server via command execution
  mcp-command:
    type: mcp
    command: /path/to/mcp-server
    args:
      - --config
      - /path/to/config.json
    vars:
      ENV_VAR: value
```

**Built-in MCP Server:**

The platform automatically exposes its tools via MCP protocol at `/mcp` endpoint, allowing other MCP clients to discover and use platform capabilities.

#### Built-in Tools

```yaml
tools:
  search:
    type: search
    retriever: web

  extract:
    type: extract
    extractor: tika

  translate:
    type: translate
    translator: default

  render:
    type: render
    renderer: dalle-3

  synthesize:
    type: synthesize
    synthesizer: tts-1
```


#### Custom Tools

```yaml
tools:
  custom-tool:
    type: custom
    url: http://localhost:8080
```


### Authentication

#### Static Authentication

```yaml
authorizers:
  - type: static
    tokens:
      - "your-secret-token"
```


#### OIDC Authentication

```yaml
authorizers:
  - type: oidc
    url: https://your-oidc-provider.com
    audience: your-audience
```


### Routing & Load Balancing

#### Round-robin Load Balancer

```yaml
routers:
  llama-lb:
    type: roundrobin
    models:
      - llama-3-8b
      - groq-llama-3-8b
      - huggingface-llama-3-8b
```


### Rate Limiting

Add rate limiting to any provider:

```yaml
providers:
  - type: openai
    token: sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
    limit: 10  # requests per second

    models:
      gpt-4o:
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

```yaml
translators:
  default:
    type: default
    # Uses configured chat models for translation
```