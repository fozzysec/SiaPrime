package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"strings"

	"github.com/spf13/cobra"

	"SiaPrime/build"
	"SiaPrime/node/api/client"
)

var (
	// Flags.
	hostContractOutputType string // output type for host contracts
	hostVerbose            bool   // display additional host info
	initForce              bool   // destroy and re-encrypt the wallet on init if it already exists
	initPassword           bool   // supply a custom password when creating a wallet
	renterAllContracts     bool   // Show all active and expired contracts
	renterDownloadAsync    bool   // Downloads files asynchronously
	renterListVerbose      bool   // Show additional info about uploaded files.
	renterShowHistory      bool   // Show download history in addition to download queue.
	siaDir                 string // Path to sia data dir
	walletRawTxn           bool   // Encode/decode transactions in base64-encoded binary.
)

var (
	// Globals.
	rootCmd    *cobra.Command // Root command cobra object, used by bash completion cmd.
	httpClient client.Client
)

// Exit codes.
// inspired by sysexits.h
const (
	exitCodeGeneral = 1  // Not in sysexits.h, but is standard practice.
	exitCodeUsage   = 64 // EX_USAGE in sysexits.h
)

// post makes an API call and discards the response. An error is returned if
// the response status is not 2xx.
// wrap wraps a generic command with a check that the command has been
// passed the correct number of arguments. The command must take only strings
// as arguments.
func wrap(fn interface{}) func(*cobra.Command, []string) {
	fnVal, fnType := reflect.ValueOf(fn), reflect.TypeOf(fn)
	if fnType.Kind() != reflect.Func {
		panic("wrapped function has wrong type signature")
	}
	for i := 0; i < fnType.NumIn(); i++ {
		if fnType.In(i).Kind() != reflect.String {
			panic("wrapped function has wrong type signature")
		}
	}

	return func(cmd *cobra.Command, args []string) {
		if len(args) != fnType.NumIn() {
			cmd.UsageFunc()(cmd)
			os.Exit(exitCodeUsage)
		}
		argVals := make([]reflect.Value, fnType.NumIn())
		for i := range args {
			argVals[i] = reflect.ValueOf(args[i])
		}
		fnVal.Call(argVals)
	}
}

// die prints its arguments to stderr, then exits the program with the default
// error code.
func die(args ...interface{}) {
	fmt.Fprintln(os.Stderr, args...)
	os.Exit(exitCodeGeneral)
}

func main() {
	root := &cobra.Command{
		Use:   os.Args[0],
		Short: "Sia Client v" + build.Version,
		Long:  "Sia Client v" + build.Version,
		Run:   wrap(consensuscmd),
	}

	rootCmd = root

	// create command tree
	root.AddCommand(versionCmd)
	root.AddCommand(stopCmd)

	root.AddCommand(updateCmd)
	updateCmd.AddCommand(updateCheckCmd)

	root.AddCommand(hostCmd)
	hostCmd.AddCommand(hostConfigCmd, hostAnnounceCmd, hostFolderCmd, hostContractCmd, hostSectorCmd)
	hostFolderCmd.AddCommand(hostFolderAddCmd, hostFolderRemoveCmd, hostFolderResizeCmd)
	hostSectorCmd.AddCommand(hostSectorDeleteCmd)
	hostCmd.Flags().BoolVarP(&hostVerbose, "verbose", "v", false, "Display detailed host info")
	hostContractCmd.Flags().StringVarP(&hostContractOutputType, "type", "t", "value", "Select output type")

	root.AddCommand(hostdbCmd)
	hostdbCmd.AddCommand(hostdbViewCmd)
	hostdbCmd.Flags().IntVarP(&hostdbNumHosts, "numhosts", "n", 0, "Number of hosts to display from the hostdb")
	hostdbCmd.Flags().BoolVarP(&hostdbVerbose, "verbose", "v", false, "Display full hostdb information")

	root.AddCommand(minerCmd)
	minerCmd.AddCommand(minerStartCmd, minerStopCmd)

	root.AddCommand(poolCmd)
	poolCmd.AddCommand(poolConfigCmd, poolClientsCmd)

	root.AddCommand(stratumminerCmd)
	stratumminerCmd.AddCommand(stratumminerStartCmd, stratumminerStopCmd)

	root.AddCommand(walletCmd)
	walletCmd.AddCommand(walletAddressCmd, walletAddressesCmd, walletChangepasswordCmd, walletInitCmd, walletInitSeedCmd,
		walletLoadCmd, walletLockCmd, walletSeedsCmd, walletSendCmd, walletSweepCmd, walletSignCmd,
		walletBalanceCmd, walletBroadcastCmd, walletTransactionsCmd, walletUnlockCmd)
	walletInitCmd.Flags().BoolVarP(&initPassword, "password", "p", false, "Prompt for a custom password")
	walletInitCmd.Flags().BoolVarP(&initForce, "force", "", false, "destroy the existing wallet and re-encrypt")
	walletInitSeedCmd.Flags().BoolVarP(&initForce, "force", "", false, "destroy the existing wallet")
	walletSendCmd.AddCommand(walletSendSiacoinsCmd, walletSendSiafundsCmd)
	walletUnlockCmd.Flags().BoolVarP(&initPassword, "password", "p", false, "Display interactive password prompt even if SIAPRIME_WALLET_PASSWORD is set")
	walletBroadcastCmd.Flags().BoolVarP(&walletRawTxn, "raw", "", false, "Decode transaction as base64 instead of JSON")
	walletSignCmd.Flags().BoolVarP(&walletRawTxn, "raw", "", false, "Encode signed transaction as base64 instead of JSON")

	root.AddCommand(renterCmd)
	renterCmd.AddCommand(renterFilesDeleteCmd, renterFilesDownloadCmd,
		renterDownloadsCmd, renterAllowanceCmd, renterSetAllowanceCmd,
		renterContractsCmd, renterFilesListCmd, renterFilesRenameCmd,
		renterFilesUploadCmd, renterUploadsCmd, renterExportCmd,
		renterPricesCmd)

	renterContractsCmd.AddCommand(renterContractsViewCmd)
	renterAllowanceCmd.AddCommand(renterAllowanceCancelCmd)

	renterCmd.Flags().BoolVarP(&renterListVerbose, "verbose", "v", false, "Show additional file info such as redundancy")
	renterContractsCmd.Flags().BoolVarP(&renterAllContracts, "all", "A", false, "Show all expired contracts in addition to active contracts")
	renterDownloadsCmd.Flags().BoolVarP(&renterShowHistory, "history", "H", false, "Show download history in addition to the download queue")
	renterFilesDownloadCmd.Flags().BoolVarP(&renterDownloadAsync, "async", "A", false, "Download file asynchronously")
	renterFilesListCmd.Flags().BoolVarP(&renterListVerbose, "verbose", "v", false, "Show additional file info such as redundancy")
	renterExportCmd.AddCommand(renterExportContractTxnsCmd)

	root.AddCommand(gatewayCmd)
	gatewayCmd.AddCommand(gatewayConnectCmd, gatewayDisconnectCmd, gatewayAddressCmd, gatewayListCmd)

	root.AddCommand(consensusCmd)

	utilsCmd.AddCommand(bashcomplCmd, mangenCmd, utilsHastingsCmd, utilsEncodeRawTxnCmd, utilsDecodeRawTxnCmd,
		utilsSigHashCmd, utilsCheckSigCmd)
	root.AddCommand(utilsCmd)

	// initialize client
	root.PersistentFlags().StringVarP(&httpClient.Address, "addr", "a", "localhost:4280", "which host/port to communicate with (i.e. the host/port spd is listening on)")
	root.PersistentFlags().StringVarP(&httpClient.Password, "apipassword", "", "", "the password for the API's http authentication")
	root.PersistentFlags().StringVarP(&siaDir, "siaprime-directory", "d", build.DefaultSiaDir(), "location of the sia directory")
	root.PersistentFlags().StringVarP(&httpClient.UserAgent, "useragent", "", "SiaPrime-Agent", "the useragent used by spc to connect to the daemon's API")

	// Check if the api password environment variable is set.
	apiPassword := os.Getenv("SIAPRIME_API_PASSWORD")
	if apiPassword != "" {
		httpClient.Password = apiPassword
		fmt.Println("Using SIAPRIME_API_PASSWORD environment variable")
	}

	// If the API password wasn't set we try to read it from the file. If
	// reading fails, continue silently; but in the next release, this will
	// be an error.
	if httpClient.Password == "" {
		pw, err := ioutil.ReadFile(build.APIPasswordFile(siaDir))
		if err == nil {
			httpClient.Password = strings.TrimSpace(string(pw))
		}
	}

	// run
	if err := root.Execute(); err != nil {
		// Since no commands return errors (all commands set Command.Run instead of
		// Command.RunE), Command.Execute() should only return an error on an
		// invalid command or flag. Therefore Command.Usage() was called (assuming
		// Command.SilenceUsage is false) and we should exit with exitCodeUsage.
		os.Exit(exitCodeUsage)
	}
}
