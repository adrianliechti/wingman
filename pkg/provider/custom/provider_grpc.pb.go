// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.5.1
// - protoc             v5.29.3
// source: provider.proto

package custom

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.64.0 or later.
const _ = grpc.SupportPackageIsVersion9

const (
	Completer_Complete_FullMethodName = "/provider.Completer/Complete"
)

// CompleterClient is the client API for Completer service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type CompleterClient interface {
	Complete(ctx context.Context, in *CompleteRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[Completion], error)
}

type completerClient struct {
	cc grpc.ClientConnInterface
}

func NewCompleterClient(cc grpc.ClientConnInterface) CompleterClient {
	return &completerClient{cc}
}

func (c *completerClient) Complete(ctx context.Context, in *CompleteRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[Completion], error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	stream, err := c.cc.NewStream(ctx, &Completer_ServiceDesc.Streams[0], Completer_Complete_FullMethodName, cOpts...)
	if err != nil {
		return nil, err
	}
	x := &grpc.GenericClientStream[CompleteRequest, Completion]{ClientStream: stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type Completer_CompleteClient = grpc.ServerStreamingClient[Completion]

// CompleterServer is the server API for Completer service.
// All implementations must embed UnimplementedCompleterServer
// for forward compatibility.
type CompleterServer interface {
	Complete(*CompleteRequest, grpc.ServerStreamingServer[Completion]) error
	mustEmbedUnimplementedCompleterServer()
}

// UnimplementedCompleterServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedCompleterServer struct{}

func (UnimplementedCompleterServer) Complete(*CompleteRequest, grpc.ServerStreamingServer[Completion]) error {
	return status.Errorf(codes.Unimplemented, "method Complete not implemented")
}
func (UnimplementedCompleterServer) mustEmbedUnimplementedCompleterServer() {}
func (UnimplementedCompleterServer) testEmbeddedByValue()                   {}

// UnsafeCompleterServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to CompleterServer will
// result in compilation errors.
type UnsafeCompleterServer interface {
	mustEmbedUnimplementedCompleterServer()
}

func RegisterCompleterServer(s grpc.ServiceRegistrar, srv CompleterServer) {
	// If the following call pancis, it indicates UnimplementedCompleterServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&Completer_ServiceDesc, srv)
}

func _Completer_Complete_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(CompleteRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(CompleterServer).Complete(m, &grpc.GenericServerStream[CompleteRequest, Completion]{ServerStream: stream})
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type Completer_CompleteServer = grpc.ServerStreamingServer[Completion]

// Completer_ServiceDesc is the grpc.ServiceDesc for Completer service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Completer_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "provider.Completer",
	HandlerType: (*CompleterServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Complete",
			Handler:       _Completer_Complete_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "provider.proto",
}

const (
	Embedder_Embed_FullMethodName = "/provider.Embedder/Embed"
)

// EmbedderClient is the client API for Embedder service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type EmbedderClient interface {
	Embed(ctx context.Context, in *EmbedRequest, opts ...grpc.CallOption) (*Embeddings, error)
}

type embedderClient struct {
	cc grpc.ClientConnInterface
}

func NewEmbedderClient(cc grpc.ClientConnInterface) EmbedderClient {
	return &embedderClient{cc}
}

func (c *embedderClient) Embed(ctx context.Context, in *EmbedRequest, opts ...grpc.CallOption) (*Embeddings, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(Embeddings)
	err := c.cc.Invoke(ctx, Embedder_Embed_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// EmbedderServer is the server API for Embedder service.
// All implementations must embed UnimplementedEmbedderServer
// for forward compatibility.
type EmbedderServer interface {
	Embed(context.Context, *EmbedRequest) (*Embeddings, error)
	mustEmbedUnimplementedEmbedderServer()
}

// UnimplementedEmbedderServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedEmbedderServer struct{}

func (UnimplementedEmbedderServer) Embed(context.Context, *EmbedRequest) (*Embeddings, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Embed not implemented")
}
func (UnimplementedEmbedderServer) mustEmbedUnimplementedEmbedderServer() {}
func (UnimplementedEmbedderServer) testEmbeddedByValue()                  {}

// UnsafeEmbedderServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to EmbedderServer will
// result in compilation errors.
type UnsafeEmbedderServer interface {
	mustEmbedUnimplementedEmbedderServer()
}

func RegisterEmbedderServer(s grpc.ServiceRegistrar, srv EmbedderServer) {
	// If the following call pancis, it indicates UnimplementedEmbedderServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&Embedder_ServiceDesc, srv)
}

func _Embedder_Embed_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(EmbedRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EmbedderServer).Embed(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: Embedder_Embed_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EmbedderServer).Embed(ctx, req.(*EmbedRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// Embedder_ServiceDesc is the grpc.ServiceDesc for Embedder service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Embedder_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "provider.Embedder",
	HandlerType: (*EmbedderServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Embed",
			Handler:    _Embedder_Embed_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "provider.proto",
}
