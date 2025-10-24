package cli

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"
)

// GetTxCmd returns the transaction commands for this module
func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      fmt.Sprintf("%s transactions subcommands", types.ModuleName),
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		CmdDepositToEscrow(),
		CmdRequestWithdrawal(),
		CmdPayForFibre(),
		CmdPaymentPromiseTimeout(),
		CmdUpdateFibreParams(),
	)

	return cmd
}

// CmdDepositToEscrow implements the deposit-to-escrow transaction command.
func CmdDepositToEscrow() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deposit-to-escrow [amount]",
		Args:  cobra.ExactArgs(1),
		Short: "Deposit tokens to an escrow account",
		Long: `Deposit tokens to an escrow account for use with fibre payments.

Example:
$ celestia-appd tx fibre deposit-to-escrow 1000000utia --from mykey
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			amount, err := sdk.ParseCoinNormalized(args[0])
			if err != nil {
				return fmt.Errorf("invalid amount: %w", err)
			}

			msg := &types.MsgDepositToEscrow{
				Signer: clientCtx.GetFromAddress().String(),
				Amount: amount,
			}

			if err := msg.ValidateBasic(); err != nil {
				return err
			}

			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)

	return cmd
}

// CmdRequestWithdrawal implements the request-withdrawal transaction command.
func CmdRequestWithdrawal() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request-withdrawal [amount]",
		Args:  cobra.ExactArgs(1),
		Short: "Request withdrawal from an escrow account",
		Long: `Request withdrawal from an escrow account. The withdrawal will be available after the unbonding period.

Example:
$ celestia-appd tx fibre request-withdrawal 1000000utia --from mykey
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			amount, err := sdk.ParseCoinNormalized(args[0])
			if err != nil {
				return fmt.Errorf("invalid amount: %w", err)
			}

			msg := &types.MsgRequestWithdrawal{
				Signer: clientCtx.GetFromAddress().String(),
				Amount: amount,
			}

			if err := msg.ValidateBasic(); err != nil {
				return err
			}

			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)

	return cmd
}

// CmdPayForFibre implements the pay-for-fibre transaction command.
func CmdPayForFibre() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pay-for-fibre [payment-promise-json] [validator-signatures]",
		Args:  cobra.ExactArgs(2),
		Short: "Process a payment promise with validator signatures",
		Long: `Process a payment promise with validator signatures for fibre data availability.

The payment-promise-json should be a JSON representation of the PaymentPromise.
The validator-signatures should be a comma-separated list of validator signatures.

Example:
$ celestia-appd tx fibre pay-for-fibre '{"signer_public_key": "...", "namespace": "...", "commitment": "...", "blob_size": 1024, "signature": "..."}' "sig1,sig2,sig3" --from mykey
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			var paymentPromise types.PaymentPromise
			if err := clientCtx.Codec.UnmarshalJSON([]byte(args[0]), &paymentPromise); err != nil {
				return fmt.Errorf("failed to unmarshal payment promise: %w", err)
			}

			// Parse validator signatures
			sigStrings := strings.Split(args[1], ",")
			validatorSignatures := make([][]byte, len(sigStrings))
			for i, sigStr := range sigStrings {
				sigStr = strings.TrimSpace(sigStr)
				if sigStr == "" {
					return fmt.Errorf("empty signature at index %d", i)
				}
				// Assume signatures are hex-encoded
				sigStr = strings.TrimPrefix(sigStr, "0x")
				sig, err := hex.DecodeString(sigStr)
				if err != nil {
					return fmt.Errorf("invalid hex signature at index %d: %w", i, err)
				}
				validatorSignatures[i] = sig
			}

			msg := &types.MsgPayForFibre{
				Signer:              clientCtx.GetFromAddress().String(),
				PaymentPromise:      paymentPromise,
				ValidatorSignatures: validatorSignatures,
			}

			if err := msg.ValidateBasic(); err != nil {
				return err
			}

			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)

	return cmd
}

// CmdPaymentPromiseTimeout implements the payment-promise-timeout transaction command.
func CmdPaymentPromiseTimeout() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "payment-promise-timeout [payment-promise-json]",
		Args:  cobra.ExactArgs(1),
		Short: "Process a timed-out payment promise",
		Long: `Process a timed-out payment promise to refund the escrow account.

Example:
$ celestia-appd tx fibre payment-promise-timeout '{"signer_public_key": "...", "namespace": "...", "commitment": "...", "blob_size": 1024, "signature": "..."}' --from mykey
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			var paymentPromise types.PaymentPromise
			if err := clientCtx.Codec.UnmarshalJSON([]byte(args[0]), &paymentPromise); err != nil {
				return fmt.Errorf("failed to unmarshal payment promise: %w", err)
			}

			msg := &types.MsgPaymentPromiseTimeout{
				Signer:         clientCtx.GetFromAddress().String(),
				PaymentPromise: paymentPromise,
			}

			if err := msg.ValidateBasic(); err != nil {
				return err
			}

			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)

	return cmd
}

// CmdUpdateFibreParams implements the update-params transaction command.
func CmdUpdateFibreParams() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update-params [gas-per-blob-byte] [withdrawal-delay-seconds] [payment-promise-timeout-seconds] [payment-promise-retention-window-seconds]",
		Args:  cobra.ExactArgs(4),
		Short: "Update fibre module parameters (governance only)",
		Long: `Update fibre module parameters. This command can only be executed by the governance module.

Example:
$ celestia-appd tx fibre update-params 10 1209600 3600 86400 --from governance
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			gasPerBlobByte, err := strconv.ParseUint(args[0], 10, 32)
			if err != nil {
				return fmt.Errorf("invalid gas per blob byte: %w", err)
			}

			withdrawalDelaySeconds, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid withdrawal delay: %w", err)
			}

			paymentPromiseTimeoutSeconds, err := strconv.ParseUint(args[2], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid payment promise timeout: %w", err)
			}

			paymentPromiseRetentionWindowSeconds, err := strconv.ParseUint(args[3], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid payment promise retention window: %w", err)
			}

			params := types.Params{
				GasPerBlobByte:                uint32(gasPerBlobByte),
				WithdrawalDelay:               time.Duration(withdrawalDelaySeconds) * time.Second,
				PaymentPromiseTimeout:         time.Duration(paymentPromiseTimeoutSeconds) * time.Second,
				PaymentPromiseRetentionWindow: time.Duration(paymentPromiseRetentionWindowSeconds) * time.Second,
			}

			msg := &types.MsgUpdateFibreParams{
				Authority: clientCtx.GetFromAddress().String(),
				Params:    params,
			}

			if err := msg.ValidateBasic(); err != nil {
				return err
			}

			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)

	return cmd
}
