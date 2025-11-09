package fibre

import (
	"context"
	"io"
	"log/slog"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/celestiaorg/celestia-app/v6/pkg/appconsts"
	"github.com/celestiaorg/celestia-app/v6/pkg/user"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	grpctypes "google.golang.org/grpc"
)

type (
	stubQueryClient struct {
		paramsResp *types.QueryParamsResponse
		escrowResp *types.QueryEscrowAccountResponse
		paramsErr  error
		escrowErr  error
	}

	stubTxClient struct {
		addr         sdk.AccAddress
		broadcasts   [][]sdk.Msg
		broadcastErr error
		confirmErr   error
	}
)

func newStubTxClient() *stubTxClient {
	const accAddrLen = 20
	addr := make(sdk.AccAddress, accAddrLen)
	copy(addr, []byte("fibre-escrow-test"))
	return &stubTxClient{addr: addr}
}

func (m *stubTxClient) DefaultAddress() sdk.AccAddress { return m.addr }

func (m *stubTxClient) BroadcastTx(_ context.Context, msgs []sdk.Msg, _ ...user.TxOption) (*sdk.TxResponse, error) {
	if m.broadcastErr != nil {
		return nil, m.broadcastErr
	}
	m.broadcasts = append(m.broadcasts, msgs)
	return &sdk.TxResponse{TxHash: "hash"}, nil
}

func (m *stubTxClient) ConfirmTx(_ context.Context, _ string) (*user.TxResponse, error) {
	if m.confirmErr != nil {
		return nil, m.confirmErr
	}
	return &user.TxResponse{}, nil
}

func (m *stubTxClient) GRPCConn() grpctypes.ClientConnInterface { return nil }

func (s *stubQueryClient) Params(context.Context, *types.QueryParamsRequest, ...grpctypes.CallOption) (*types.QueryParamsResponse, error) {
	if s.paramsErr != nil {
		return nil, s.paramsErr
	}
	return s.paramsResp, nil
}

func (s *stubQueryClient) EscrowAccount(context.Context, *types.QueryEscrowAccountRequest, ...grpctypes.CallOption) (*types.QueryEscrowAccountResponse, error) {
	if s.escrowErr != nil {
		return nil, s.escrowErr
	}
	return s.escrowResp, nil
}

func TestEnsureEscrowFundsDepositsWhenMissing(t *testing.T) {
	ctx := context.Background()
	query := &stubQueryClient{
		paramsResp: &types.QueryParamsResponse{Params: types.Params{GasPerBlobByte: 2}},
		escrowResp: &types.QueryEscrowAccountResponse{EscrowAccount: &types.EscrowAccount{}, Found: false},
	}
	tx := newStubTxClient()
	client := &Client{
		cfg:         ClientConfig{AutoFundEscrow: true},
		txClient:    tx,
		queryClient: query,
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	required := sdk.NewCoin(appconsts.BondDenom, sdkmath.NewInt(2048))
	require.NoError(t, client.ensureEscrowFunds(ctx, 1024))
	require.Len(t, tx.broadcasts, 1)
	msg, ok := tx.broadcasts[0][0].(*types.MsgDepositToEscrow)
	require.True(t, ok)
	require.Equal(t, tx.DefaultAddress().String(), msg.Signer)
	require.Equal(t, required, msg.Amount)
}

func TestEnsureEscrowFundsSkipsWhenSufficient(t *testing.T) {
	ctx := context.Background()
	available := sdk.NewCoin(appconsts.BondDenom, sdkmath.NewInt(4096))
	query := &stubQueryClient{
		paramsResp: &types.QueryParamsResponse{Params: types.Params{GasPerBlobByte: 2}},
		escrowResp: &types.QueryEscrowAccountResponse{
			Found: true,
			EscrowAccount: &types.EscrowAccount{
				AvailableBalance: available,
			},
		},
	}
	tx := newStubTxClient()
	client := &Client{
		cfg:         ClientConfig{AutoFundEscrow: true},
		txClient:    tx,
		queryClient: query,
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	require.NoError(t, client.ensureEscrowFunds(ctx, 2048))
	require.Len(t, tx.broadcasts, 0)
}

func TestEnsureEscrowFundsRespectsAutoFundFlag(t *testing.T) {
	ctx := context.Background()
	client := &Client{
		cfg:         ClientConfig{AutoFundEscrow: false},
		txClient:    newStubTxClient(),
		queryClient: &stubQueryClient{},
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	require.NoError(t, client.ensureEscrowFunds(ctx, 1024))
}
