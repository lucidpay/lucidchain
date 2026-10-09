package cli

// TODO: remove this file once attestation tested

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"

	"github.com/lucidpay/lucidchain/x/attestor/types"
)

const (
	flagClaimFile      = "claim-file"
	flagSignaturesFile = "signatures-file"
	flagCheckpointHash = "checkpoint-hash"
)

// GetTxCmd returns the hand-written tx commands of the attestor module.
// autocli merges them with the generated ones (EnhanceCustomCommand: true).
func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "attestor transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(CmdSignBytes(), CmdSubmitAttestation())
	return cmd
}

type sigJSON struct {
	SignerIndex uint32 `json:"signer_index"`
	Signature   string `json:"signature"` // hex
}

// CmdSignBytes prints the canonical bytes (hex) the attestor's signers must sign.
func CmdSignBytes() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sign-bytes [schema-id] [attestor-id] [sidechain-id] [checkpoint-sequence]",
		Short: "Print the hex sign bytes for an attestation (sign these offline)",
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			if clientCtx.ChainID == "" {
				return fmt.Errorf("chain id is required: pass --%s or set it in client.toml", flags.FlagChainID)
			}
			seq, err := strconv.ParseUint(args[3], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid checkpoint sequence: %w", err)
			}
			claimFile, _ := cmd.Flags().GetString(flagClaimFile)
			claim, err := os.ReadFile(claimFile) // raw bytes, never re-encode
			if err != nil {
				return err
			}
			hashHex, _ := cmd.Flags().GetString(flagCheckpointHash)
			cpHash, err := hex.DecodeString(hashHex)
			if err != nil || len(cpHash) == 0 {
				return fmt.Errorf("--%s must be non-empty hex", flagCheckpointHash)
			}

			res, err := types.NewQueryClient(clientCtx).Attestor(cmd.Context(),
				&types.QueryAttestorRequest{Id: args[1]})
			if err != nil {
				return err
			}

			bz, err := types.AttestationSignBytes(&types.AttestationSignDoc{
				ChainId:            clientCtx.ChainID,
				SchemaId:           args[0],
				AttestorId:         args[1],
				SidechainId:        args[2],
				CheckpointSequence: seq,
				CheckpointHash:     cpHash,
				ClaimPayload:       claim,
				SignerSetVersion:   res.Attestor.SignerSetVersion,
			})
			if err != nil {
				return err
			}
			return clientCtx.PrintString(hex.EncodeToString(bz) + "\n")
		},
	}
	cmd.Flags().String(flagClaimFile, "", "path to the claim JSON file")
	cmd.Flags().String(flagCheckpointHash, "", "checkpoint hash (hex) from x/checkpoint")
	_ = cmd.MarkFlagRequired(flagClaimFile)
	_ = cmd.MarkFlagRequired(flagCheckpointHash)
	flags.AddQueryFlagsToCmd(cmd)
	if cmd.Flags().Lookup(flags.FlagChainID) == nil {
		cmd.Flags().String(flags.FlagChainID, "", "network chain ID")
	}
	return cmd
}

// CmdSubmitAttestation broadcasts MsgSubmitAttestation. --from must be the attestor's operator.
func CmdSubmitAttestation() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit-attestation [schema-id] [attestor-id] [sidechain-id] [checkpoint-sequence]",
		Short: "Submit a signed attestation for a checkpoint",
		Long: `signatures-file format:
  [{"signer_index":0,"signature":"<hex>"}, ...]`,
		Args: cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			seq, err := strconv.ParseUint(args[3], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid checkpoint sequence: %w", err)
			}
			claimFile, _ := cmd.Flags().GetString(flagClaimFile)
			claim, err := os.ReadFile(claimFile)
			if err != nil {
				return err
			}

			sigFile, _ := cmd.Flags().GetString(flagSignaturesFile)
			raw, err := os.ReadFile(sigFile)
			if err != nil {
				return err
			}
			var in []sigJSON
			if err := json.Unmarshal(raw, &in); err != nil {
				return fmt.Errorf("invalid signatures file: %w", err)
			}
			sigs := make([]types.Signature, 0, len(in))
			for _, s := range in {
				b, err := hex.DecodeString(s.Signature)
				if err != nil {
					return fmt.Errorf("signer_index %d: bad hex signature: %w", s.SignerIndex, err)
				}
				sigs = append(sigs, types.Signature{SignerIndex: s.SignerIndex, Signature: b})
			}

			res, err := types.NewQueryClient(clientCtx).Attestor(cmd.Context(),
				&types.QueryAttestorRequest{Id: args[1]})
			if err != nil {
				return err
			}

			msg := &types.MsgSubmitAttestation{
				Submitter:          clientCtx.GetFromAddress().String(),
				SchemaId:           args[0],
				AttestorId:         args[1],
				SidechainId:        args[2],
				CheckpointSequence: seq,
				ClaimPayload:       claim,
				SignerSetVersion:   res.Attestor.SignerSetVersion,
				Signatures:         sigs,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().String(flagClaimFile, "", "path to the claim JSON file (same bytes that were signed)")
	cmd.Flags().String(flagSignaturesFile, "", "path to the signatures JSON file")
	_ = cmd.MarkFlagRequired(flagClaimFile)
	_ = cmd.MarkFlagRequired(flagSignaturesFile)
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}
