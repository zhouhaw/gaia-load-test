package main

import (
	"flag"
	"fmt"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/gatechain/gatechain-load-test/account"
	"github.com/gatechain/gatechain-load-test/internal/flags"
	"github.com/spf13/viper"
	"github.com/tendermint/tendermint/libs/cli"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/kit/log/term"

	cmn "github.com/tendermint/tendermint/libs/common"
	"github.com/tendermint/tendermint/libs/log"
	tmrpc "github.com/tendermint/tendermint/rpc/client"
)

var logger = log.NewNopLogger()

func main() {
	var durationInt, txsRate, connections, txSize int
	var verbose bool
	var outputFormat, broadcastTxMethod string
	var home, chainId, passWord string

	flagSet := flag.NewFlagSet("gaia-load-test", flag.ExitOnError)
	flagSet.StringVar(&home, "home", "$HOME/.gaiacli", "directory for config and data")
	flagSet.IntVar(&connections, "c", 1, "Connections to keep open per endpoint")
	flagSet.IntVar(&durationInt, "T", 20, "Exit after the specified amount of time in seconds")
	flagSet.IntVar(&txsRate, "r", 2000, "Txs per second to send in a connection")
	flagSet.IntVar(&txSize, "s", 250, "The size of a transaction in bytes, must be greater than or equal to 40.")
	flagSet.StringVar(&outputFormat, "output-format", "plain", "Output format: plain or json")
	flagSet.StringVar(&broadcastTxMethod, "broadcast-tx-method", "async", "Broadcast method: async (no guarantees; fastest), sync (ensures tx is checked) or commit (ensures tx is checked and committed; slowest)")
	flagSet.BoolVar(&verbose, "v", false, "Verbose output")
	flagSet.StringVar(&chainId, "chain-id", "testing", "the chain-id of gatechain")
	flagSet.StringVar(&passWord, "p", "1234567890", "the password of account")

	flagSet.Usage = func() {
		fmt.Println(`gaia blockchain benchmarking tool.

Usage:
	./gaia-load-test [-c 1] [-T 10] [-r 1000] [-s 250] [endpoints] [-output-format <plain|json> [-broadcast-tx-method <async|sync|commit>]]

Examples:
	./gaia-load-test -home /Users/zhouhaw/.gaiacli -chain-id testing -p 1234567890 
	localhost:26657 cosmos15khq8csn68qxwju50trsn2zj4n9w5466y9ely3`)
		fmt.Println("Flags:")
		flagSet.PrintDefaults()
	}

	flagSet.Parse(os.Args[1:])

	if flagSet.NArg() == 0 {
		flagSet.Usage()
		os.Exit(1)
	}

	if verbose {
		if outputFormat == "json" {
			printErrorAndExit("Verbose mode not supported with json output.")
		}
		// Color errors red
		colorFn := func(keyvals ...interface{}) term.FgBgColor {
			for i := 1; i < len(keyvals); i += 2 {
				if _, ok := keyvals[i].(error); ok {
					return term.FgBgColor{Fg: term.White, Bg: term.Red}
				}
			}
			return term.FgBgColor{}
		}
		logger = log.NewTMLoggerWithColorFn(log.NewSyncWriter(os.Stdout), colorFn)

		fmt.Printf("Running %ds test @ %s\n", durationInt, flagSet.Arg(0))
	}

	if txSize < 40 {
		printErrorAndExit("The size of a transaction must be greater than or equal to 40.")
	}

	if broadcastTxMethod != "async" &&
		broadcastTxMethod != "sync" &&
		broadcastTxMethod != "commit" {
		printErrorAndExit("broadcast-tx-method should be either 'sync', 'async' or 'commit'.")
	}
	viper.Set(cli.HomeFlag, home)
	viper.Set(client.FlagChainID, chainId)
	viper.Set(flags.FlagPassword, passWord)

	var (
		endpoints     = strings.Split(flagSet.Arg(0), ",")
		fromAddr      = strings.Split(flagSet.Arg(1), ",")
		client        = tmrpc.NewHTTP(endpoints[0], "/websocket")
		initialHeight = latestBlockHeight(client)
	)
	logger.Info("Latest block height", "h", initialHeight)

	transacters, timeStart := startTransacters(
		endpoints,
		connections,
		txsRate,
		txSize,
		"broadcast_tx_"+broadcastTxMethod,
		client,
		client,
		fromAddr,
	)

	// Stop upon receiving SIGTERM or CTRL-C.
	cmn.TrapSignal(logger, func() {
		for _, t := range transacters {
			t.Stop()
		}
	})

	// Wait until transacters have begun until we get the start time.
	// improve the startTime precise
	//timeStart := time.Now()
	logger.Info("Time last transacter started", "t", timeStart)

	duration := time.Duration(durationInt) * time.Second

	timeEnd := timeStart.Add(duration)
	logger.Info("End time for calculation", "t", timeEnd)

	<-time.After(duration)
	for i, t := range transacters {
		t.Stop()
		numCrashes := countCrashes(t.connsBroken)
		if numCrashes != 0 {
			fmt.Printf("%d connections crashed on transacter #%d\n", numCrashes, i)
		}
	}

	logger.Debug("Time all transacters stopped", "t", time.Now())

	stats, err := calculateStatistics(
		client,
		initialHeight,
		timeStart,
		durationInt,
	)
	if err != nil {
		printErrorAndExit(err.Error())
	}

	printStatistics(stats, outputFormat)
}

func latestBlockHeight(client tmrpc.Client) int64 {
	status, err := client.Status()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return status.SyncInfo.LatestBlockHeight
}

func countCrashes(crashes []bool) int {
	count := 0
	for i := 0; i < len(crashes); i++ {
		if crashes[i] {
			count++
		}
	}
	return count
}

func startTransacters(
	endpoints []string,
	connections,
	txsRate int,
	txSize int,
	broadcastTxMethod string,
	client tmrpc.Client,
	mempoolClient tmrpc.MempoolClient,
	fromAddr []string,
) ([]*transacter, time.Time) {
	transacters := make([]*transacter, len(endpoints))
	var timeStart time.Time

	wg := sync.WaitGroup{}
	wg.Add(len(endpoints))
	for i, e := range endpoints {
		t := newTransacter(e, connections, txsRate, txSize, broadcastTxMethod, client, mempoolClient)
		// prepare tx for test

		// produce different address for test
		err := account.Init()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		//infos, err := account.List()
		//if _, exist := account.FindByStr(infos, fromAddr[0]); !exist {
		//	fmt.Println("validator account not exist in local")
		//	os.Exit(1)
		//} else {
		//	if len(infos) < 4000 {
		//		infosNew, err := account.CreateBatch(5000)
		//		if err != nil {
		//			fmt.Println(infosNew)
		//			os.Exit(1)
		//		}
		//		infos = append(infos, infosNew...)
		//	}
		//}

		go t.produce(fromAddr)
		fmt.Printf("preparing the data set\n")
		//time.Sleep(10 * time.Second)
		for len(t.txChan) <= 2000 {
			//fmt.Println("sleeping....")
			fmt.Println("txChan", len(t.txChan))
			time.Sleep(1 * time.Second)
		}
		fmt.Printf("%d prepared\n", len(t.txChan))
		t.SetLogger(logger)

		timeStart = time.Now()
		go func(i int) {
			defer wg.Done()
			if err := t.Start(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			transacters[i] = t
		}(i)
	}
	wg.Wait()

	return transacters, timeStart
}

func printErrorAndExit(err string) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
