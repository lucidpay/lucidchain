package main

import (
	"fmt"
	"os"

	_ "cosmossdk.io/api/cosmos/crypto/mldsa65"
	clienthelpers "cosmossdk.io/client/v2/helpers"
	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"

	"github.com/lucidpay/lucidchain/app"
	"github.com/lucidpay/lucidchain/cmd/lucidchaind/cmd"
)

func main() {
	rootCmd := cmd.NewRootCmd()
	if err := svrcmd.Execute(rootCmd, clienthelpers.EnvPrefix, app.DefaultNodeHome); err != nil {
		fmt.Fprintln(rootCmd.OutOrStderr(), err)
		os.Exit(1)
	}
}
