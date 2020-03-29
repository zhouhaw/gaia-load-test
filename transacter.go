package main

import (
	"encoding/json"
	"fmt"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keys"
	"github.com/cosmos/cosmos-sdk/x/auth"
	context "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/bank"
	"github.com/cosmos/gaia/app"
	"github.com/spf13/viper"
	"github.com/tendermint/tendermint/crypto/secp256k1"
	"runtime"

	// it is ok to use math/rand here: we do not need a cryptographically secure random
	// number generator here and we can run the tests a bit faster
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	util "github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	tmcrypto "github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/libs/log"
	tmrpc "github.com/tendermint/tendermint/rpc/client"
	rpctypes "github.com/tendermint/tendermint/rpc/lib/types"
)

const (
	sendTimeout = 10 * time.Second
	// see https://github.com/tendermint/tendermint/blob/master/rpc/lib/server/handlers.go
	pingPeriod      = (30 * 9 / 10) * time.Second
	defaultPassword = "123456780"
)

type transacter struct {
	Target            string
	Rate              int
	Size              int
	Connections       int
	BroadcastTxMethod string

	conns       []*websocket.Conn
	connsBroken []bool
	startingWg  sync.WaitGroup
	endingWg    sync.WaitGroup
	stopped     bool

	logger        log.Logger
	client        tmrpc.Client
	mempoolClient tmrpc.MempoolClient
	mtx           sync.Mutex
	txChan        chan []byte
}

func newTransacter(target string, connections, rate int, size int, broadcastTxMethod string, client tmrpc.Client, mempoolClient tmrpc.MempoolClient) *transacter {
	return &transacter{
		Target:            target,
		Rate:              rate,
		Size:              size,
		Connections:       connections,
		BroadcastTxMethod: broadcastTxMethod,
		conns:             make([]*websocket.Conn, connections),
		connsBroken:       make([]bool, connections),
		logger:            log.NewNopLogger(),
		client:            client,
		mempoolClient:     mempoolClient,
		txChan:            make(chan []byte, 200*rate),
	}
}

// SetLogger lets you set your own logger
func (t *transacter) SetLogger(l log.Logger) {
	t.logger = l
}

// Start opens N = `t.Connections` connections to the target and creates read
// and write goroutines for each connection.
func (t *transacter) Start() error {
	t.stopped = false
	for i := 0; i < t.Connections; i++ {
		t.Target = "localhost:26657"
		c, _, err := connect(t.Target)
		if err != nil {
			return err
		}
		t.conns[i] = c
	}

	t.startingWg.Add(t.Connections)
	t.endingWg.Add(2 * t.Connections)
	for i := 0; i < t.Connections; i++ {
		go t.sendLoop(i)
		go t.receiveLoop(i)
	}

	t.startingWg.Wait()

	return nil
}

// Stop closes the connections.
func (t *transacter) Stop() {
	t.stopped = true
	t.endingWg.Wait()
	for _, c := range t.conns {
		c.Close()
	}
}

// receiveLoop reads messages from the connection (empty in case of
// `broadcast_tx_async`).
func (t *transacter) receiveLoop(connIndex int) {
	c := t.conns[connIndex]
	defer t.endingWg.Done()
	for {
		_, _, err := c.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				t.logger.Error(
					fmt.Sprintf("failed to read response on conn %d", connIndex),
					"err",
					err,
				)
			}
			return
		}
		if t.stopped || t.connsBroken[connIndex] {
			return
		}
	}
}

// sendLoop generates transactions at a given rate.
func (t *transacter) sendLoop(connIndex int) {
	started := false
	// Close the starting waitgroup, in the event that this fails to start
	defer func() {
		if !started {
			t.startingWg.Done()
		}
	}()
	c := t.conns[connIndex]

	c.SetPingHandler(func(message string) error {
		err := c.WriteControl(websocket.PongMessage, []byte(message), time.Now().Add(sendTimeout))
		if err == websocket.ErrCloseSent {
			return nil
		} else if e, ok := err.(net.Error); ok && e.Temporary() {
			return nil
		}
		return err
	})

	logger := t.logger.With("addr", c.RemoteAddr())

	pingsTicker := time.NewTicker(pingPeriod)
	txsTicker := time.NewTicker(1 * time.Second)
	mempoolTicker := time.NewTicker(800 * time.Millisecond)
	defer func() {
		pingsTicker.Stop()
		txsTicker.Stop()
		mempoolTicker.Stop()
		t.endingWg.Done()
	}()

	// each transaction embeds connection index, tx number and hash of the hostname
	// we update the tx number between successive txs

	for {
		select {
		case <-txsTicker.C:
			startTime := time.Now()
			endTime := startTime.Add(time.Second)
			numTxSent := t.Rate
			if !started {
				t.startingWg.Done()
				started = true
			}
			now := time.Now()
			//fmt.Printf("time: %s . txs number:%d \n",now.String(),t.Rate)
			for i := 0; i < t.Rate; i++ {
				if len(t.txChan) < 100 {
					fmt.Println("tx chan < 100!")
				}
				tx := <-t.txChan
				//select {
				//case x := <-t.txChan:
				//	tx = x
				//case x2 := <-t.txChan2:
				//	tx = x2
				//case x3 := <-t.txChan3:
				//	tx = x3
				//}
				paramsJSON, err := json.Marshal(map[string]interface{}{"tx": tx}) //txHex
				if err != nil {
					fmt.Printf("failed to encode params: %v\n", err)
					os.Exit(1)
				}
				rawParamsJSON := json.RawMessage(paramsJSON)

				//c.SetWriteDeadline(now.Add(sendTimeout))
				err = c.WriteJSON(rpctypes.RPCRequest{
					JSONRPC: "2.0",
					ID:      rpctypes.JSONRPCStringID("jsonrpc-client"),
					Method:  t.BroadcastTxMethod,
					Params:  rawParamsJSON,
				})
				if err != nil {
					fmt.Println("jsonrpc err ", err)
					err = errors.Wrap(err,
						fmt.Sprintf("txs send failed on connection #%d", connIndex))
					t.connsBroken[connIndex] = true
					fmt.Println("Error ---------------------", err.Error())
					return
				}

				if i%5 == 0 {
					// cache the time.Now() reads to save time.
					now = time.Now()
					if now.After(endTime) {
						// Plus one accounts for sending this tx
						numTxSent = i + 1
						break
					}
				}
			}

			timeToSend := time.Since(startTime)
			fmt.Println(fmt.Sprintf("sent %d transactions", numTxSent), "took", timeToSend) //logger.Info(
			if timeToSend < 1*time.Second {
				sleepTime := time.Second - timeToSend
				//fmt.Println(fmt.Sprintf("connection #%d is sleeping for %f seconds", connIndex, sleepTime.Seconds()))
				time.Sleep(sleepTime)
			}
			go func() {
				for {
					time.Sleep(800 * time.Millisecond)
					memRespone, err := t.mempoolClient.NumUnconfirmedTxs()
					if err != nil {
						fmt.Println("memepoolclient error", err)
					} else {
						fmt.Println("memepool info", "tx count", memRespone.Count, "number", memRespone.Total, "total size", memRespone.TotalBytes)
					}
				}
			}()
			//memRespone, err := t.mempoolClient.NumUnconfirmedTxs()
			//if err != nil {
			//	fmt.Println("memepoolclient error", err)
			//} else {
			//	fmt.Println("memepool info", "tx count", memRespone.Count, "number", memRespone.Total, "total size", memRespone.TotalBytes)
			//}
		case <-pingsTicker.C:
			// go-rpc server closes the connection in the absence of pings
			c.SetWriteDeadline(time.Now().Add(sendTimeout))
			if err := c.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				err = errors.Wrap(err,
					fmt.Sprintf("failed to write ping message on conn #%d", connIndex))
				logger.Error(err.Error())
				t.connsBroken[connIndex] = true
			}
		}

		if t.stopped {
			// To cleanly close a connection, a client should send a close
			// frame and wait for the server to close the connection.
			c.SetWriteDeadline(time.Now().Add(sendTimeout))
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				err = errors.Wrap(err,
					fmt.Sprintf("failed to write close message on conn #%d", connIndex))
				logger.Error(err.Error())
				t.connsBroken[connIndex] = true
			}

			return
		}
	}
}

func connect(host string) (*websocket.Conn, *http.Response, error) {
	u := url.URL{Scheme: "ws", Host: host, Path: "/websocket"}
	return websocket.DefaultDialer.Dial(u.String(), nil)
}

func (t *transacter) produce(fromStr []string) {
	count := len(fromStr)
	var wg = new(sync.WaitGroup)
	wg.Add(count) // 增加计数值
	runtime.GOMAXPROCS(4)

	kb, err := util.NewKeyBaseFromHomeFlag()
	if err != nil {
		fmt.Println("home dir error ", err)
		return
	}

	for i := 0; i < count; i++ {
		fromAddr, err := sdk.AccAddressFromBech32(fromStr[i])
		if err != nil {
			fmt.Println(err)
			return
		}
		info, err := kb.GetByAddress(fromAddr)
		if err != nil {
			fmt.Println("home dir or address error:", err)
			return
		}
		var priv tmcrypto.PrivKey
		passphrase := viper.GetString(defaultPassword)
		priv, err = kb.ExportPrivateKeyObject(info.GetName(), passphrase)
		if err != nil {
			fmt.Println(err)
			return
		}
		go func() {
			time.Sleep(time.Duration(i*100) * time.Millisecond)
			t.produceOne(info, t.txChan, wg, kb, priv)
		}()
	}
	wg.Wait()
}

func (t *transacter) produceOne(info keys.Info, res chan []byte, wg *sync.WaitGroup, kb keys.Keybase, priv tmcrypto.PrivKey) {
	defer wg.Done() // 计数值减一
	var sequenceNumber uint64 = 0
	privKey := secp256k1.GenPrivKey()
	pub := privKey.PubKey()
	toAddr := types.AccAddress(pub.Address())

	fromAddr := info.GetAddress()
	account, err := t.queryAccount(fromAddr)
	if err != nil {
		fmt.Println("fromaddr err", err)
		return
	}
	sequenceNumber = account.GetSequence()
	accountNumber := account.GetAccountNumber()

	coins := []types.Coin{types.NewInt64Coin("stake", 2)}

	for true {
		txbyte := t.generateSendTx(kb, info, toAddr, coins, accountNumber, sequenceNumber, priv)
		//fmt.Printf("%s sequense now: %d %d\n", info.GetAddress(), sequenceNumber, len(txbyte))
		sequenceNumber++
		res <- txbyte
	}
	return
}

func (t *transacter) queryAccount(addr types.AccAddress) (auth.Account, error) {
	cdc := app.MakeCodec()
	bz, err := cdc.MarshalJSON(auth.NewQueryAccountParams(addr))
	if err != nil {
		return nil, err
	}
	route := fmt.Sprintf("custom/acc/%s", auth.QueryAccount)

	status, err := t.client.Status()
	if err != nil {
		return nil, err
	}

	opts := tmrpc.ABCIQueryOptions{
		Height: status.SyncInfo.LatestBlockHeight,
		Prove:  true,
	}

	res, err := t.client.ABCIQueryWithOptions(route, bz, opts)
	if err != nil {
		return nil, err
	}

	resp := res.Response
	if !resp.IsOK() {
		return nil, errors.New(resp.Log)
	}

	var account auth.Account
	if err := cdc.UnmarshalJSON(resp.Value, &account); err != nil {
		return nil, err
	}

	return account, nil
}

func (t *transacter) generateSendTx(kb keys.Keybase, from keys.Info, to types.AccAddress,
	coins types.Coins, accountNumber uint64, sequence uint64, priv tmcrypto.PrivKey) []byte {

	msg := bank.MsgSend{from.GetAddress(), to, coins}

	fee := auth.NewStdFee(5000000, []types.Coin{types.NewInt64Coin("stake", 1)})

	cdc := app.MakeCodec()
	stdSignMsg := context.StdSignMsg{
		ChainID:       viper.GetString(client.FlagChainID),
		AccountNumber: accountNumber,
		Sequence:      sequence,
		Fee:           fee,
		Msgs:          []types.Msg{msg},
		Memo:          "tps test",
	}
	pubkey := priv.PubKey()
	sigBytes, err := priv.Sign(stdSignMsg.Bytes())
	if err != nil {
		return nil
	}
	sig := auth.StdSignature{PubKey: pubkey,
		Signature: sigBytes}

	signedStdTx := auth.NewStdTx([]types.Msg{msg}, fee, []auth.StdSignature{sig}, "tps test")
	signedByte, err := cdc.MarshalBinaryLengthPrefixed(signedStdTx)
	return signedByte
}
