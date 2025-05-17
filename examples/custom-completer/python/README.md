### Generate gRPC Server & Messages

```shell
pip install grpcio-tools grpcio-reflection
```

```shell
$ curl -Lo provider.proto https://raw.githubusercontent.com/adrianliechti/wingman/refs/heads/main/pkg/provider/custom/provider.proto
$ python -m grpc_tools.protoc -I . --python_out=. --pyi_out=. --grpc_python_out=. provider.proto
```

### Run this Tool

```shell
$ python main.go
> Tool Server started. Listening on port 50051
```

### Example Configuration

```yaml
providers:
  - type: custom
    url: grpc://localhost:50051
    models:
      test:
        type: completer
```