package keeper_test

import (
	"github.com/cometbft/cometbft/crypto/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// MockBankKeeper implements the expected BankKeeper interface for testing
type MockBankKeeper struct {
	SendCoinsFromAccountToModuleFn func(ctx sdk.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
}

func (m *MockBankKeeper) SendCoinsFromAccountToModule(ctx sdk.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error {
	if m.SendCoinsFromAccountToModuleFn != nil {
		return m.SendCoinsFromAccountToModuleFn(ctx, senderAddr, recipientModule, amt)
	}
	return nil
}

func (m *MockBankKeeper) SendCoinsFromModuleToAccount(ctx sdk.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error {
	return nil
}

func (m *MockBankKeeper) GetModuleAddress(moduleName string) sdk.AccAddress {
	return authtypes.NewModuleAddress(moduleName)
}

// MockStakingKeeper implements the expected StakingKeeper interface for testing
type MockStakingKeeper struct {
	historicalInfo map[int64]stakingtypes.HistoricalInfo
	validatorKeys  map[int64]ed25519.PrivKey
}

func (m *MockStakingKeeper) GetHistoricalInfo(ctx sdk.Context, height int64) (stakingtypes.HistoricalInfo, error) {
	if m.historicalInfo != nil {
		if info, ok := m.historicalInfo[height]; ok {
			return info, nil
		}
	}
	return stakingtypes.HistoricalInfo{}, nil
}
