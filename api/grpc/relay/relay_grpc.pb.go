// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.3.0
// - protoc             v4.23.4
// source: relay/relay.proto

package relay

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

const (
	Relay_GetChunk_FullMethodName = "/node.Relay/GetChunk"
)

// RelayClient is the client API for Relay service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type RelayClient interface {
	// GetChunk retrieves a specific chunk for a blob custodied at the Node.
	GetChunk(ctx context.Context, in *GetChunkRequest, opts ...grpc.CallOption) (*GetChunkReply, error)
}

type relayClient struct {
	cc grpc.ClientConnInterface
}

func NewRelayClient(cc grpc.ClientConnInterface) RelayClient {
	return &relayClient{cc}
}

func (c *relayClient) GetChunk(ctx context.Context, in *GetChunkRequest, opts ...grpc.CallOption) (*GetChunkReply, error) {
	out := new(GetChunkReply)
	err := c.cc.Invoke(ctx, Relay_GetChunk_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RelayServer is the server API for Relay service.
// All implementations must embed UnimplementedRelayServer
// for forward compatibility
type RelayServer interface {
	// GetChunk retrieves a specific chunk for a blob custodied at the Node.
	GetChunk(context.Context, *GetChunkRequest) (*GetChunkReply, error)
	mustEmbedUnimplementedRelayServer()
}

// UnimplementedRelayServer must be embedded to have forward compatible implementations.
type UnimplementedRelayServer struct {
}

func (UnimplementedRelayServer) GetChunk(context.Context, *GetChunkRequest) (*GetChunkReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetChunk not implemented")
}
func (UnimplementedRelayServer) mustEmbedUnimplementedRelayServer() {}

// UnsafeRelayServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to RelayServer will
// result in compilation errors.
type UnsafeRelayServer interface {
	mustEmbedUnimplementedRelayServer()
}

func RegisterRelayServer(s grpc.ServiceRegistrar, srv RelayServer) {
	s.RegisterService(&Relay_ServiceDesc, srv)
}

func _Relay_GetChunk_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetChunkRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RelayServer).GetChunk(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: Relay_GetChunk_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RelayServer).GetChunk(ctx, req.(*GetChunkRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// Relay_ServiceDesc is the grpc.ServiceDesc for Relay service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Relay_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "node.Relay",
	HandlerType: (*RelayServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetChunk",
			Handler:    _Relay_GetChunk_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "relay/relay.proto",
}