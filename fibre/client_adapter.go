package fibre

import (
	"context"
	"io"

	fibredrpc "github.com/celestiaorg/celestia-app/v6/fibre/drpc"
	fibregrpc "github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	core "github.com/cometbft/cometbft/types"
	"storj.io/drpc"
)

// TransportClient is a common interface for both gRPC and DRPC Fibre clients.
// It provides methods for uploading and downloading rows using either transport.
type TransportClient interface {
	io.Closer
	// UploadRows uploads rows to the validator using the underlying transport.
	UploadRows(ctx context.Context, req *types.UploadRowsRequest) (*types.UploadRowsResponse, error)
	// DownloadRows downloads rows from the validator using the underlying transport.
	DownloadRows(ctx context.Context, req *types.DownloadRowsRequest) (*types.DownloadRowsResponse, error)
}

// TransportClientFn is a constructor function that creates a [TransportClient]
// for a given validator. It abstracts over both gRPC and DRPC client constructors.
type TransportClientFn func(ctx context.Context, val *core.Validator) (TransportClient, error)

// grpcClientAdapter adapts a gRPC Fibre client to the [TransportClient] interface.
type grpcClientAdapter struct {
	fibregrpc.Client
}

func (g *grpcClientAdapter) UploadRows(ctx context.Context, req *types.UploadRowsRequest) (*types.UploadRowsResponse, error) {
	return g.Client.UploadRows(ctx, req)
}

func (g *grpcClientAdapter) DownloadRows(ctx context.Context, req *types.DownloadRowsRequest) (*types.DownloadRowsResponse, error) {
	return g.Client.DownloadRows(ctx, req)
}

// NewGRPCClientFn creates a [TransportClientFn] from a gRPC [fibregrpc.NewClientFn].
func NewGRPCClientFn(fn fibregrpc.NewClientFn) TransportClientFn {
	return func(ctx context.Context, val *core.Validator) (TransportClient, error) {
		client, err := fn(ctx, val)
		if err != nil {
			return nil, err
		}
		return &grpcClientAdapter{Client: client}, nil
	}
}

// drpcClientAdapter adapts a DRPC Fibre client to the [TransportClient] interface.
type drpcClientAdapter struct {
	client fibredrpc.Client
}

func (d *drpcClientAdapter) UploadRows(ctx context.Context, req *types.UploadRowsRequest) (*types.UploadRowsResponse, error) {
	var resp *types.UploadRowsResponse
	err := d.client.DoDrpc(ctx, func(conn drpc.Conn) error {
		fibreClient := types.NewDRPCFibreClient(conn)
		var err error
		resp, err = fibreClient.UploadRows(ctx, req)
		return err
	})
	return resp, err
}

func (d *drpcClientAdapter) DownloadRows(ctx context.Context, req *types.DownloadRowsRequest) (*types.DownloadRowsResponse, error) {
	var resp *types.DownloadRowsResponse
	err := d.client.DoDrpc(ctx, func(conn drpc.Conn) error {
		fibreClient := types.NewDRPCFibreClient(conn)
		var err error
		resp, err = fibreClient.DownloadRows(ctx, req)
		return err
	})
	return resp, err
}

func (d *drpcClientAdapter) Close() error {
	return d.client.Close()
}

// NewDRPCClientFn creates a [TransportClientFn] from a DRPC [fibredrpc.NewClientFn].
func NewDRPCClientFn(fn fibredrpc.NewClientFn) TransportClientFn {
	return func(ctx context.Context, val *core.Validator) (TransportClient, error) {
		client, err := fn(ctx, val)
		if err != nil {
			return nil, err
		}
		return &drpcClientAdapter{client: client}, nil
	}
}
