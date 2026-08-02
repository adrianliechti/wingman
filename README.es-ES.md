

# Wingman

<img src="docs/icon.png" width="150"/>

**Una plataforma unificada de LLM: una API, múltiples proveedores, cero bloqueo del proveedor.**

Wingman es un centro de inferencia de código abierto que simplifica la creación y el despliegue a escala de aplicaciones de modelos de lenguaje grande (LLM). Interfaza con todos los principales proveedores de modelos y entornos de ejecución locales a través de una única API compatible con OpenAI, Anthropic y Gemini, con RAG, agentes, herramientas, MCP, enrutamiento, limitación de tasa y OpenTelemetry integrados únicamente mediante configuración.

## Características principales

### Soporte para múltiples proveedores

La plataforma se integra con una amplia gama de proveedores de LLM:

**Modelos de chat/completado:**
- Plataforma OpenAI y Servicio Azure OpenAI (modelos GPT)
- Anthropic (modelos Claude)
- Google Gemini
- AWS Bedrock
- Mistral AI
- xAI (modelos Grok)
- OpenRouter, NVIDIA NIM y cualquier endpoint compatible con OpenAI
- Despliegues locales: Ollama, LLAMA.CPP
- Modelos personalizados mediante plugins gRPC

**Modelos de incrustación (embeddings):**
- OpenAI, Azure OpenAI, Google Gemini, Mistral AI
- Local: Ollama, LLAMA.CPP
- Incrustadores personalizados mediante gRPC

**Procesamiento de medios:**
- Generación de imágenes: OpenAI DALL-E, Google Gemini, xAI
- Voz a texto: OpenAI Whisper, Mistral, Azure Speech
- Texto a voz: OpenAI TTS, Azure Speech, xAI

### Procesamiento de documentos y RAG

**Extractores de documentos:**
- Azure Document Intelligence
- Docling para conversión de documentos
- Kreuzberg para análisis de documentos
- Extracción de documentos de Mistral
- Extracción basada en LLM utilizando cualquier modelo de visión/chat
- Extracción de texto desde archivos planos
- Extractores personalizados mediante gRPC

**Segmentación de texto:**
- Segmentador Kreuzberg
- Fragmentación basada en texto con tamaños configurables
- Segmentadores personalizados mediante gRPC

**Recuperación de información:**
- Búsqueda web: DuckDuckGo, Exa, Tavily
- Recuperadores personalizados mediante plugins gRPC

### Flujos de trabajo avanzados de IA

**Cadenas y agentes:**
- Cadenas de agentes/asistentes con capacidades de llamadas a herramientas
- Flujos de conversación personalizados
- Flujos de trabajo de razonamiento multipaso
- Integración de herramientas y llamadas a funciones

**Herramientas y llamadas a funciones:**
- Herramientas integradas: búsqueda, extracción web, investigación, traducción
- **Soporte para Model Context Protocol (MCP)**: Implementación completa de servidor y cliente
  - Conéctese a servidores MCP externos como proveedores de herramientas
  - Servidor MCP integrado que expone las capacidades de la plataforma
  - Múltiples métodos de transporte (streaming HTTP, SSE)
- Herramientas personalizadas mediante plugins gRPC

**Capacidades adicionales:**
- Resumen de texto (mediante modelos de chat)
- Traducción de idiomas
- Renderizado y formato de contenido

### Infraestructura y operaciones

**Enrutamiento y balanceo de carga:**
- Balanceador de carga round-robin para distribuir solicitudes
- Estrategias de conmutación por error de modelos
- Enrutamiento de solicitudes entre múltiples proveedores

**Limitación de tasa y control:**
- Limitación de tasa por proveedor y por modelo
- Control de flujo y colas de solicitudes
- Controles de uso de recursos

**Autenticación y seguridad:**
- Autenticación con tokens estáticos
- Integración con OpenID Connect (OIDC)
- Gestión segura de credenciales

**Compatibilidad de API:**
- Endpoints de API compatibles con OpenAI
- Configuraciones de API personalizadas
- Soporte para múltiples versiones de API

**Observabilidad y monitoreo:**
- Integración completa con OpenTelemetry
- Tracing de solicitudes en todos los componentes
- Métricas y registros exhaustivos
- Monitoreo de rendimiento y depuración

### Configuración flexible

Los desarrolladores pueden definir proveedores, modelos, credenciales, pipelines de procesamiento de documentos, herramientas y flujos de trabajo avanzados de IA utilizando archivos de configuración YAML. Este enfoque simplifica la integración y facilita la gestión de aplicaciones de IA complejas.


## Arquitectura

![Architecture](docs/architecture.png)

> Fuente: [`docs/architecture.html`](docs/architecture.html) · Regenerar con `task docs:render`.

La arquitectura está diseñada para ser modular y extensible, permitiendo a los desarrolladores integrar diferentes proveedores y servicios según sea necesario. Se compone de los siguientes componentes clave:

**Proveedores principales:**
- **Completers**: Modelos de chat/completado para generación de texto y razonamiento
- **Embedders**: Modelos de incrustación vectorial para comprensión semántica
- **Renderers**: Generación de imágenes y creación de contenido visual
- **Synthesizers**: Texto a voz y generación de audio
- **Transcribers**: Voz a texto y procesamiento de audio
- **Rerankers**: Clasificación de resultados y puntuación de relevancia

**Procesamiento de documentos y datos:**
- **Extractors**: Análisis de documentos y extracción de contenido desde diversos formatos
- **Segmenters**: Fragmentación de texto y segmentación semántica para RAG
- **Retrievers**: Búsqueda web y recuperación de información
- **Summarizers**: Compresión y resumen de contenido
- **Translators**: Traducción de texto multilingüe

**Flujos de trabajo de IA y herramientas:**
- **Chains**: Flujos de trabajo de IA multipaso y razonamiento basado en agentes
- **Tools**: Llamadas a funciones, búsqueda web, procesamiento de documentos y capacidades personalizadas
- **APIs**: Múltiples formatos de API y capas de compatibilidad

**Infraestructura:**
- **Routers**: Balanceo de carga y distribución de solicitudes
- **Rate Limiters**: Control de recursos y control de flujo
- **Authorizers**: Autenticación y control de acceso
- **Observability**: Tracing y monitoreo con OpenTelemetry

## Casos de uso

- **Aplicaciones empresariales de IA**: Plataforma unificada para múltiples servicios y modelos de IA
- **RAG (Generación Aumentada por Recuperación)**: Procesamiento de documentos, búsqueda semántica y recuperación de conocimiento
- **Agentes y flujos de trabajo de IA**: Razonamiento multipaso, integración de herramientas y ejecución autónoma de tareas
- **Despliegue escalable de LLM**: Aplicaciones de alto volumen con balanceo de carga y conmutación por error
- **IA multimodal**: Combinación de capacidades de procesamiento de texto, imagen y audio
- **Pipelines de IA personalizados**: Flujos de trabajo flexibles utilizando herramientas y cadenas personalizadas


## Inicio rápido

Todo se gestiona a través de un único `config.yaml`. Defina los proveedores y, luego, añada herramientas, agentes y pipelines según sea necesario.

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

Ejecute el servidor (lee `.env` para los secretos referenciados):

```shell
task server        # or: go run cmd/server/main.go
```

Llámelo con cualquier cliente compatible con OpenAI: los agentes aparecen como modelos regulares:

```shell
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{ "model": "wingman", "messages": [{ "role": "user", "content": "What changed in the news today?" }] }'
```

### Superficie de API

Un único punto de entrada habla cuatro dialectos, por lo que los SDK existentes funcionan sin cambios:

| Familia | Montaje | Endpoints |
| --- | --- | --- |
| **OpenAI** (compatible) | `/v1` | `chat/completions`, `responses`, `embeddings`, `audio/{speech,transcriptions}`, `images/{generations,edits}`, `models` |
| **Anthropic** (compatible) | `/v1` | `messages`, `messages/count_tokens` |
| **Gemini** (compatible) | `/v1beta` | `models/{model}:generateContent`, `:streamGenerateContent`, `:countTokens` |
| **MCP** (nativo) | `/v1` | `mcp/{name}` — cada servidor MCP configurado, vía HTTP-stream o SSE |
| **Wingman** (nativo) | `/v1` | `extract`, `segment`, `search`, `retrieve`, `research`, `rerank`, `summarize`, `translate`, `render`, `transcribe` |


## Integraciones y configuración

### Proveedores de LLM

#### Plataforma OpenAI

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


#### Servicio Azure OpenAI

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

Texto a voz y voz a texto utilizando Azure Cognitive Services Speech. Soporta voces multilingües con detección automática de idioma. Los nombres de voz de OpenAI (alloy, echo, fable, nova, onyx, shimmer) se mapean automáticamente a sus equivalentes de Azure.

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

La variable `region` se utiliza para construir los endpoints apropiados:
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


#### OpenRouter y endpoints compatibles con OpenAI

Cualquier endpoint compatible con OpenAI (OpenRouter, vLLM, LM Studio, NVIDIA NIM, una puerta de enlace autohospedada, …) funciona apuntando `url` hacia él. Utilice el proveedor `openai` para un endpoint plug-and-play, o `openrouter` / `nim` donde exista un adaptador dedicado.

```yaml
providers:
  - type: openai
    url: https://openrouter.ai/api/v1
    token: ${OPENROUTER_API_KEY}

    models:
      glm-air:
        id: z-ai/glm-4.6-air
```


> **Interfaces de proveedores.** Cada modelo cumple uno de seis roles, inferidos desde su `type` o establecidos explícitamente por modelo: **completer** (chat/razonamiento), **embedder** (vectores), **renderer** (texto→imagen), **synthesizer** (texto→voz), **transcriber** (voz→texto), **reranker** (relevancia). Consulte [`docs/architecture.png`](docs/architecture.png) para la matriz completa de interfaz × backend.


### Enrutadores

Un enrutador expone varios modelos bajo un mismo identificador y distribuye las solicitudes entre ellos — útil para balanceo de carga y conmutación por error entre proveedores. Tipos: `roundrobin` (rotación uniforme) y `adaptive` (prefiere backends saludables/rápidos).

Los enrutadores protegen los backends con un interruptor automático (circuit breaker) y realizan la conmutación por error de forma transparente: si un proveedor falla o no produce salida dentro de `first_token_timeout` (predeterminado `2m`), la solicitud se reintentará en el siguiente proveedor saludable antes de que cualquier error llegue al cliente.

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
> Establezca `max_retries: 0` en los modelos utilizados como miembros de un enrutador. Los SDK de proveedores reintentan los límites de tasa en su lugar (honrando `Retry-After`, lo que puede significar esperar 30s+ en el mismo backend) — deshabilitar los reintentos del SDK permite al enrutador conmutar inmediatamente a otro backend.


### Acceso web (Búsqueda · Extracción · Investigación)

El acceso web viene en tres modalidades. Un **buscador** devuelve listas de resultados, un **extractor web** obtiene y limpia una sola URL, y un **investigador** ejecuta un ciclo completo de investigación multipaso. Cada uno se referencia por nombre desde `tools` (consulte [Herramientas y llamadas a funciones](#herramientas-y-llamadas-a-funciones)).

#### Buscadores

Devuelven resultados de búsqueda clasificados. Tipos: `duckduckgo`, `exa`, `tavily`, `custom`.

```yaml
searchers:
  web:
    type: exa            # or: duckduckgo · tavily · custom
    token: ${EXA_API_KEY}
```

#### Extractores web (Scrapers)

Obtención y extracción de contenido limpio desde una URL. Tipos: `fetch` (HTTP integrado), `exa`, `tavily`, `custom`.

```yaml
scrapers:
  web:
    type: fetch          # or: exa · tavily · custom

  reader:
    type: tavily
    token: ${TAVILY_API_KEY}
```

#### Investigadores

Ejecutan un flujo de investigación de extremo a extremo. Tipos: `exa`, `openai`, `anthropic`, `perplexity`, `custom`, o el `agent` integrado que orquesta su propio modelo con un buscador + extractor.

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


### Extracción de documentos

#### Azure Document Intelligence

```yaml
extractors:
  azure:
    type: azure
    url: https://YOUR_INSTANCE.cognitiveservices.azure.com
    token: ${AZURE_API_KEY}
```


#### Extractor Docling

https://github.com/DS4SD/docling

```yaml
extractors:
  docling:
    type: docling
    url: http://localhost:5000
```


#### Extractor Kreuzberg

https://github.com/lenskit/kreuzberg

```yaml
extractors:
  kreuzberg:
    type: kreuzberg
    url: http://localhost:8000
```


#### Extractor Mistral

```yaml
extractors:
  mistral:
    type: mistral
    token: ${MISTRAL_API_KEY}
```


#### Extractor LLM

Use cualquier modelo de visión/chat configurado para extraer el contenido del documento.

```yaml
extractors:
  llm:
    type: llm
    model: gpt-5.4-mini
```


#### Extractor de texto

```yaml
extractors:
  text:
    type: text
```


#### Extractor personalizado

```yaml
extractors:
  custom:
    type: custom
    url: http://localhost:8080
```


### Segmentación de texto

#### Segmentador Kreuzberg

```yaml
segmenters:
  kreuzberg:
    type: kreuzberg
    url: http://localhost:8000
```


#### Segmentador de texto

```yaml
segmenters:
  text:
    type: text
    chunkSize: 1000
    chunkOverlap: 200
```


#### Segmentador personalizado

```yaml
segmenters:
  custom:
    type: custom
    url: http://localhost:8080
```


### Agentes de IA

Los agentes envuelven un completer con un prompt de sistema, herramientas y un bucle de control, y luego se exponen como un identificador de modelo regular (use la clave del agente como `model` en cualquier solicitud). Hay dos tipos de bucle disponibles:

- **`assistant`** — un bucle de llamadas a herramientas que ejecuta herramientas hasta que el modelo produce una respuesta final.
- **`react`** — un bucle explícito de razón → actuar → observar.

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

Los prompts de sistema son plantillas de Go — los asistentes como `{{ now | date "2006-01-02" }}` se evalúan por solicitud.


### Herramientas y llamadas a funciones

#### Protocolo de contexto de modelo (MCP)

La plataforma proporciona soporte integral para el Model Context Protocol (MCP), habilitando la integración con herramientas y servicios compatibles con MCP.

**Soporte de servidor MCP:**
- Servidor MCP integrado que expone herramientas de la plataforma a clientes MCP
- Descubrimiento automático de herramientas y generación de esquemas
- Múltiples métodos de transporte (streaming HTTP, SSE, línea de comandos)

**Soporte de cliente MCP:**
- Conéctese a servidores MCP externos como proveedores de herramientas
- Soporte para varios métodos de transporte MCP
- Registro y ejecución automática de herramientas

**Consumir un servidor MCP externo como herramientas** — apunte una herramienta `mcp` a cualquier endpoint MCP de streaming HTTP o SSE; sus herramientas se descubren y registran automáticamente:

```yaml
tools:
  # HTTP streaming (/mcp) or SSE (/sse) — transport is auto-detected
  github:
    type: mcp
    url: https://api.example.com/mcp
    vars:
      api-key: ${API_KEY}   # forwarded as a header to the server
```

**Exponer sus propias herramientas como un servidor MCP** — agrupe herramientas bajo `mcps`; cada una se sirve en `/v1/mcp/{name}` para que cualquier cliente MCP (IDEs, agentes) la consuma:

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

#### Herramientas integradas

Las herramientas integradas envuelven los proveedores que configuró en otras partes. Tipos válidos: `search`, `scraper` (alias `crawler`), `research`, `translator`, `mcp`, `custom`.

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


#### Herramientas personalizadas

```yaml
tools:
  custom-tool:
    type: custom
    url: http://localhost:8080
```


### Autenticación

Los autorizadores se ejecutan como middleware en cada solicitud. Sin ninguno configurado, el acceso es abierto. Tipos: `anonymous`, `header`, `static`, `oidc`.

#### Tokens estáticos

```yaml
authorizers:
  - type: static
    tokens:
      - "your-secret-token"
```

#### Encabezado

Confíe en un proxy upstream que inyecte un encabezado de identidad.

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


### Limitación de tasa

Añada limitación de tasa a cualquier proveedor, con anulaciones opcionales por modelo:

```yaml
providers:
  - type: openai
    token: ${OPENAI_API_KEY}
    limit: 10  # requests per second

    models:
      gpt-5.4:
        limit: 5  # override for specific model
```


### Resumen y traducción

#### Resumen automático

El resumen está disponible automáticamente para cualquier modelo de chat:

```yaml
# Use any completer model for summarization
# The platform automatically adapts chat models for summarization tasks
```


#### Traducción

Los traductores respaldan el endpoint `/v1/translate` y la herramienta `translator`. Tipos: `deepl`, `azure`, `llm` (use cualquier completer), `custom`.

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
