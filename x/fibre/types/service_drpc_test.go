package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"storj.io/drpc"
)

// TestDRPCTypesExist verifies that the DRPC types were generated correctly
func TestDRPCTypesExist(t *testing.T) {
	// Verify client interface exists
	var _ DRPCFibreClient = (*drpcFibreClient)(nil)

	// Verify server interface exists
	var _ DRPCFibreServer = (*DRPCFibreUnimplementedServer)(nil)
}

// TestUploadRowsRequest tests creating an UploadRowsRequest
func TestUploadRowsRequest(t *testing.T) {
	promise := generatePaymentPromise(t)

	rows := &Rows{
		Rows: []*Row{
			{
				Index: 0,
				Data:  []byte("test data"),
				Proof: [][]byte{[]byte("proof1")},
			},
		},
		Rlc: &Rows_Coefficients{
			Coefficients: []byte("test coefficients"),
		},
	}

	req := &UploadRowsRequest{
		Promise: &promise,
		Rows:    rows,
	}

	require.NotNil(t, req)
	require.NotNil(t, req.Promise)
	require.NotNil(t, req.Rows)
	require.Equal(t, uint32(0), req.Rows.Rows[0].Index)
	require.Equal(t, []byte("test data"), req.Rows.Rows[0].Data)
}

// TestUploadRowsResponse tests creating an UploadRowsResponse
func TestUploadRowsResponse(t *testing.T) {
	signature := make([]byte, 32)
	for i := range signature {
		signature[i] = byte(i)
	}

	resp := &UploadRowsResponse{
		ValidatorSignature: signature,
	}

	require.NotNil(t, resp)
	require.Equal(t, 32, len(resp.ValidatorSignature))
}

// TestDownloadRowsRequest tests creating a DownloadRowsRequest
func TestDownloadRowsRequest(t *testing.T) {
	commitment := generateCommitment()

	req := &DownloadRowsRequest{
		Commitment: commitment,
	}

	require.NotNil(t, req)
	require.Equal(t, 32, len(req.Commitment))
}

// TestDownloadRowsResponse tests creating a DownloadRowsResponse
func TestDownloadRowsResponse(t *testing.T) {
	rows := &Rows{
		Rows: []*Row{
			{
				Index: 0,
				Data:  []byte("downloaded data"),
				Proof: [][]byte{[]byte("proof1"), []byte("proof2")},
			},
		},
		Rlc: &Rows_Root{
			Root: generateCommitment(), // 32 bytes
		},
	}

	resp := &DownloadRowsResponse{
		Rows: rows,
	}

	require.NotNil(t, resp)
	require.NotNil(t, resp.Rows)
	require.Equal(t, 1, len(resp.Rows.Rows))
	require.Equal(t, []byte("downloaded data"), resp.Rows.Rows[0].Data)
}

// mockFibreServer is a mock implementation of DRPCFibreServer for testing
type mockFibreServer struct {
	DRPCFibreUnimplementedServer
}

func (m *mockFibreServer) UploadRows(ctx context.Context, req *UploadRowsRequest) (*UploadRowsResponse, error) {
	return &UploadRowsResponse{
		ValidatorSignature: make([]byte, 32),
	}, nil
}

func (m *mockFibreServer) DownloadRows(ctx context.Context, req *DownloadRowsRequest) (*DownloadRowsResponse, error) {
	return &DownloadRowsResponse{
		Rows: &Rows{
			Rows: []*Row{
				{Index: 0, Data: []byte("mock data")},
			},
		},
	}, nil
}

// TestMockServer verifies the mock server implements the interface correctly
func TestMockServer(t *testing.T) {
	var server DRPCFibreServer = &mockFibreServer{}

	// Test UploadRows
	uploadReq := &UploadRowsRequest{
		Promise: &PaymentPromise{
			ChainId: "test-chain",
			Height:  100,
		},
		Rows: &Rows{},
	}
	uploadResp, err := server.UploadRows(context.Background(), uploadReq)
	require.NoError(t, err)
	require.NotNil(t, uploadResp)
	require.Equal(t, 32, len(uploadResp.ValidatorSignature))

	// Test DownloadRows
	downloadReq := &DownloadRowsRequest{
		Commitment: generateCommitment(),
	}
	downloadResp, err := server.DownloadRows(context.Background(), downloadReq)
	require.NoError(t, err)
	require.NotNil(t, downloadResp)
	require.NotNil(t, downloadResp.Rows)
	require.Equal(t, []byte("mock data"), downloadResp.Rows.Rows[0].Data)
}

// TestDRPCDescription verifies the DRPC service description
func TestDRPCDescription(t *testing.T) {
	desc := DRPCFibreDescription{}

	// Test NumMethods
	require.Equal(t, 2, desc.NumMethods())

	// Test Method retrieval
	for i := 0; i < desc.NumMethods(); i++ {
		name, encoding, receiver, handler, ok := desc.Method(i)
		require.True(t, ok)
		require.NotEmpty(t, name)
		require.NotNil(t, encoding)
		require.NotNil(t, receiver)
		require.NotNil(t, handler)
	}

	// Test out of bounds
	_, _, _, _, ok := desc.Method(999)
	require.False(t, ok)
}

// TestGogoProtobufUsage verifies that gogo/protobuf is being used (not google/protobuf)
func TestGogoProtobufUsage(t *testing.T) {
	// Create a request
	req := &UploadRowsRequest{
		Promise: &PaymentPromise{
			ChainId: "test",
			Height:  1,
		},
		Rows: &Rows{},
	}

	// Verify it implements drpc.Message (which it should via gogo proto.Message)
	var _ drpc.Message = req

	// The encoding should be using gogo protobuf
	encoding := drpcEncoding_File_celestia_fibre_v1_service_proto{}

	// Test marshal
	data, err := encoding.Marshal(req)
	require.NoError(t, err)
	require.NotNil(t, data)

	// Test unmarshal
	decoded := &UploadRowsRequest{}
	err = encoding.Unmarshal(data, decoded)
	require.NoError(t, err)
	require.Equal(t, "test", decoded.Promise.ChainId)
	require.Equal(t, int64(1), decoded.Promise.Height)
}
