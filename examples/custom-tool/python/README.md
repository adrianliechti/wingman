# Custom Tool/Plugin using Python

Extending the capabilities of the LLM Platform can be a powerful way to add custom functionality that fits your specific needs.

By writing a plugin, you can provide the model with new skills through custom code, allowing it to call specialized functions directly. This plugin-based approach turns the LLM into a more adaptable and targeted solution, seamlessly integrating new functions that expand its natural language capabilities.

In this guide, we'll walk through creating a plugin that can be invoked by the LLM as a function, enabling it to perform tasks like data retrieval, calculations, or integrations with external systems — all driven by simple user prompts.

### Generate gRPC Server & Messages

```shell
pip install grpcio-tools grpcio-reflection
```

```shell
$ curl -Lo tool.proto https://raw.githubusercontent.com/adrianliechti/wingman/refs/heads/main/pkg/tool/custom/tool.proto
$ python -m grpc_tools.protoc -I . --python_out=. --pyi_out=. --grpc_python_out=. tool.proto
```

(see here: https://grpc.io/docs/languages/python/basics/)


### Run this Tool

```shell
$ python main.go
> Tool Server started. Listening on port 50051
```

### Example Configuration

```yaml
providers:
  - type: openai
    token: ${OPENAI_API_KEY}
    models:
      - gpt-4o

tools:
  weather:
    type: custom
    url: grpc://localhost:50051

chains:
  genius:
    type: agent
    model: gpt-4o
    tools:
      - weather
```