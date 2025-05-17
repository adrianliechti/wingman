from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class CompleteRequest(_message.Message):
    __slots__ = ("tools", "messages", "effort", "max_tokens", "temperature", "top_k", "top_p", "stops", "format", "schema")
    TOOLS_FIELD_NUMBER: _ClassVar[int]
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    EFFORT_FIELD_NUMBER: _ClassVar[int]
    MAX_TOKENS_FIELD_NUMBER: _ClassVar[int]
    TEMPERATURE_FIELD_NUMBER: _ClassVar[int]
    TOP_K_FIELD_NUMBER: _ClassVar[int]
    TOP_P_FIELD_NUMBER: _ClassVar[int]
    STOPS_FIELD_NUMBER: _ClassVar[int]
    FORMAT_FIELD_NUMBER: _ClassVar[int]
    SCHEMA_FIELD_NUMBER: _ClassVar[int]
    tools: _containers.RepeatedCompositeFieldContainer[Tool]
    messages: _containers.RepeatedCompositeFieldContainer[Message]
    effort: str
    max_tokens: int
    temperature: float
    top_k: float
    top_p: float
    stops: _containers.RepeatedScalarFieldContainer[str]
    format: str
    schema: Schema
    def __init__(self, tools: _Optional[_Iterable[_Union[Tool, _Mapping]]] = ..., messages: _Optional[_Iterable[_Union[Message, _Mapping]]] = ..., effort: _Optional[str] = ..., max_tokens: _Optional[int] = ..., temperature: _Optional[float] = ..., top_k: _Optional[float] = ..., top_p: _Optional[float] = ..., stops: _Optional[_Iterable[str]] = ..., format: _Optional[str] = ..., schema: _Optional[_Union[Schema, _Mapping]] = ...) -> None: ...

class Completion(_message.Message):
    __slots__ = ("id", "model", "reason", "delta", "message", "usage")
    ID_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    DELTA_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    USAGE_FIELD_NUMBER: _ClassVar[int]
    id: str
    model: str
    reason: str
    delta: Message
    message: Message
    usage: Usage
    def __init__(self, id: _Optional[str] = ..., model: _Optional[str] = ..., reason: _Optional[str] = ..., delta: _Optional[_Union[Message, _Mapping]] = ..., message: _Optional[_Union[Message, _Mapping]] = ..., usage: _Optional[_Union[Usage, _Mapping]] = ...) -> None: ...

class Message(_message.Message):
    __slots__ = ("role", "content")
    ROLE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    role: str
    content: _containers.RepeatedCompositeFieldContainer[Content]
    def __init__(self, role: _Optional[str] = ..., content: _Optional[_Iterable[_Union[Content, _Mapping]]] = ...) -> None: ...

class Content(_message.Message):
    __slots__ = ("text", "refusal", "file", "tool_call", "tool_result")
    TEXT_FIELD_NUMBER: _ClassVar[int]
    REFUSAL_FIELD_NUMBER: _ClassVar[int]
    FILE_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_FIELD_NUMBER: _ClassVar[int]
    TOOL_RESULT_FIELD_NUMBER: _ClassVar[int]
    text: str
    refusal: str
    file: File
    tool_call: ToolCall
    tool_result: ToolResult
    def __init__(self, text: _Optional[str] = ..., refusal: _Optional[str] = ..., file: _Optional[_Union[File, _Mapping]] = ..., tool_call: _Optional[_Union[ToolCall, _Mapping]] = ..., tool_result: _Optional[_Union[ToolResult, _Mapping]] = ...) -> None: ...

class File(_message.Message):
    __slots__ = ("name", "content", "content_type")
    NAME_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    CONTENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    name: str
    content: bytes
    content_type: str
    def __init__(self, name: _Optional[str] = ..., content: _Optional[bytes] = ..., content_type: _Optional[str] = ...) -> None: ...

class Tool(_message.Message):
    __slots__ = ("name", "description", "properties")
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    PROPERTIES_FIELD_NUMBER: _ClassVar[int]
    name: str
    description: str
    properties: str
    def __init__(self, name: _Optional[str] = ..., description: _Optional[str] = ..., properties: _Optional[str] = ...) -> None: ...

class ToolCall(_message.Message):
    __slots__ = ("id", "name", "arguments")
    ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    ARGUMENTS_FIELD_NUMBER: _ClassVar[int]
    id: str
    name: str
    arguments: str
    def __init__(self, id: _Optional[str] = ..., name: _Optional[str] = ..., arguments: _Optional[str] = ...) -> None: ...

class ToolResult(_message.Message):
    __slots__ = ("id", "data")
    ID_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    id: str
    data: str
    def __init__(self, id: _Optional[str] = ..., data: _Optional[str] = ...) -> None: ...

class Schema(_message.Message):
    __slots__ = ("name", "description", "properties")
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    PROPERTIES_FIELD_NUMBER: _ClassVar[int]
    name: str
    description: str
    properties: str
    def __init__(self, name: _Optional[str] = ..., description: _Optional[str] = ..., properties: _Optional[str] = ...) -> None: ...

class Usage(_message.Message):
    __slots__ = ("input_tokens", "output_tokens")
    INPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    input_tokens: int
    output_tokens: int
    def __init__(self, input_tokens: _Optional[int] = ..., output_tokens: _Optional[int] = ...) -> None: ...

class EmbedRequest(_message.Message):
    __slots__ = ("texts",)
    TEXTS_FIELD_NUMBER: _ClassVar[int]
    texts: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, texts: _Optional[_Iterable[str]] = ...) -> None: ...

class Embeddings(_message.Message):
    __slots__ = ("id", "model", "Embeddings", "usage")
    ID_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    EMBEDDINGS_FIELD_NUMBER: _ClassVar[int]
    USAGE_FIELD_NUMBER: _ClassVar[int]
    id: str
    model: str
    Embeddings: _containers.RepeatedCompositeFieldContainer[Embedding]
    usage: Usage
    def __init__(self, id: _Optional[str] = ..., model: _Optional[str] = ..., Embeddings: _Optional[_Iterable[_Union[Embedding, _Mapping]]] = ..., usage: _Optional[_Union[Usage, _Mapping]] = ...) -> None: ...

class Embedding(_message.Message):
    __slots__ = ("data",)
    DATA_FIELD_NUMBER: _ClassVar[int]
    data: _containers.RepeatedScalarFieldContainer[float]
    def __init__(self, data: _Optional[_Iterable[float]] = ...) -> None: ...
